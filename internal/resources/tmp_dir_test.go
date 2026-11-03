/*
Copyright 2026 Paperclip Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resources

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	openclawv1alpha1 "github.com/paperclipinc/openclaw-operator/api/v1alpha1"
)

func TestBuildStatefulSet_SafeTmpDirectories(t *testing.T) {
	for _, plugins := range []bool{false, true} {
		for _, customUID := range []bool{false, true} {
			instance := newTestInstance("safe-tmp")
			if plugins {
				instance.Spec.Plugins = []string{"@openclaw/duckduckgo-plugin@2026.10.9"}
			}
			if customUID {
				instance.Spec.Security.ContainerSecurityContext = &openclawv1alpha1.ContainerSecurityContextSpec{RunAsUser: Ptr(int64(2000))}
			}
			sts := BuildStatefulSet(instance, "", nil, nil, nil)
			pod := sts.Spec.Template.Spec
			main := pod.Containers[0]
			prep := findInitContainer(pod.InitContainers, TmpDirInitContainerName)
			if prep == nil {
				t.Fatal("main /tmp preparation must always be present")
			}
			assertTmpMount(t, main, "tmp", MainTmpSubPath)
			if !reflect.DeepEqual(prep.SecurityContext.RunAsUser, main.SecurityContext.RunAsUser) {
				t.Fatal("main /tmp preparation must use the gateway UID")
			}
			if len(prep.SecurityContext.Capabilities.Add) != 0 || !*prep.SecurityContext.ReadOnlyRootFilesystem {
				t.Fatal("main /tmp preparation must not require capabilities or writable rootfs")
			}
			installer := findInitContainer(pod.InitContainers, "init-plugins")
			scratch := findInitContainer(pod.InitContainers, "init-plugin-scratch")
			if !plugins {
				if installer != nil || scratch != nil || findVolume(pod.Volumes, pluginScratchVolume) != nil {
					t.Fatal("plugin scratch must not be created without plugins")
				}
				continue
			}
			if installer == nil || scratch == nil {
				t.Fatal("plugin installer and scratch preparation must both be present")
			}
			assertTmpMount(t, *installer, pluginScratchVolume, "private")
			if scratch.SecurityContext.RunAsUser != nil || installer.SecurityContext.RunAsUser != nil {
				t.Fatal("scratch and installer must retain the pod UID used by the fork's PVC chown")
			}
			if scratch.Image != installer.Image || scratch.ImagePullPolicy != installer.ImagePullPolicy {
				t.Fatal("scratch preparation must use the installer image and pull policy")
			}
			if len(scratch.Env) != 0 || len(scratch.EnvFrom) != 0 || len(scratch.VolumeMounts) != 1 ||
				scratch.VolumeMounts[0].Name != pluginScratchVolume || scratch.VolumeMounts[0].MountPath != "/scratch" {
				t.Fatal("scratch preparation must only access its scratch volume, not runtime secrets or data")
			}
			if len(scratch.SecurityContext.Capabilities.Add) != 0 ||
				!reflect.DeepEqual(scratch.SecurityContext.Capabilities.Drop, []corev1.Capability{"ALL"}) {
				t.Fatal("scratch preparation must drop all capabilities")
			}
			if !reflect.DeepEqual(installer.SecurityContext.Capabilities.Add, []corev1.Capability{"FOWNER"}) ||
				!strings.Contains(installer.Command[2], "--accept-capabilities") ||
				pod.InitContainers[0].Name != "init-chown-home" {
				t.Fatal("fork plugin capability consent, FOWNER and PVC chown must be preserved")
			}
			for i, c := range pod.InitContainers {
				if c.Name == "init-plugins" && (i == 0 || pod.InitContainers[i-1].Name != "init-plugin-scratch") {
					t.Fatal("scratch preparation must run immediately before plugin installation")
				}
			}
		}
	}
}

func assertTmpMount(t *testing.T, container corev1.Container, volume, subPath string) {
	t.Helper()
	count := 0
	for _, mount := range container.VolumeMounts {
		if mount.MountPath == "/tmp" {
			count++
			if mount.Name != volume || mount.SubPath != subPath || mount.ReadOnly {
				t.Fatalf("%s must mount its prepared writable subdirectory at /tmp: %+v", container.Name, mount)
			}
		}
	}
	if count != 1 {
		t.Fatalf("%s must have exactly one /tmp mount, got %d", container.Name, count)
	}
}

func TestTmpPreparationScripts_PermissionsAndRetries(t *testing.T) {
	instance := newTestInstance("tmp-permissions")
	instance.Spec.Plugins = []string{"test-plugin"}
	for _, tc := range []struct {
		name      string
		container corev1.Container
		root      string
		subPath   string
		want      os.FileMode
	}{
		{"main", buildTmpDirInitContainer(instance), "/tmp-volume", MainTmpSubPath, 0o777 | os.ModeSticky},
		{"plugins", buildPluginScratchInitContainer(buildPluginsInitContainer(instance)), "/scratch", "private", 0o700},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.container.Command[0]); err != nil {
				t.Skipf("script runtime unavailable: %v", err)
			}
			root := t.TempDir()
			if err := os.Chmod(root, 0o777|os.ModeSetgid); err != nil {
				t.Fatal(err)
			}
			args := append([]string(nil), tc.container.Command[1:]...)
			args[len(args)-1] = strings.ReplaceAll(args[len(args)-1], tc.root, root)
			dir := filepath.Join(root, tc.subPath)
			for retry := 0; retry < 2; retry++ {
				cmd := exec.Command(tc.container.Command[0], args...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("preparation failed: %v: %s", err, out)
				}
				info, err := os.Stat(dir)
				if err != nil {
					t.Fatal(err)
				}
				if got := info.Mode() & (os.ModePerm | os.ModeSticky | os.ModeSetgid); got != tc.want {
					t.Fatalf("prepared directory mode = %v, want %v", got, tc.want)
				}
				if err := os.Chmod(dir, 0o777|os.ModeSetgid); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestBuildTmpDirInitContainer_RegistryOverride(t *testing.T) {
	instance := newTestInstance("tmp-registry")
	instance.Spec.Registry = "registry.example.com"
	if got := buildTmpDirInitContainer(instance).Image; got != "registry.example.com/busybox:1.37" {
		t.Fatalf("tmp preparation image = %q", got)
	}
}
