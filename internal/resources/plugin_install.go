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

import corev1 "k8s.io/api/core/v1"

const pluginScratchVolume = "plugins-tmp"

// fsGroup can make the volume root mode 2777 without sticky protection.
// Mount only a private directory owned by the installer UID at its /tmp.
func buildPluginScratchInitContainer(installer *corev1.Container) corev1.Container {
	sc := installer.SecurityContext.DeepCopy()
	sc.ReadOnlyRootFilesystem = Ptr(true)
	sc.Capabilities = &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}
	return corev1.Container{
		Name:                     "init-plugin-scratch",
		Image:                    installer.Image,
		ImagePullPolicy:          installer.ImagePullPolicy,
		Command:                  []string{"node", "-e", `const fs = require('node:fs'); fs.mkdirSync('/scratch/private', {recursive: true, mode: 0o700}); fs.chmodSync('/scratch/private', 0o700);`},
		SecurityContext:          sc,
		Resources:                *installer.Resources.DeepCopy(),
		VolumeMounts:             []corev1.VolumeMount{{Name: pluginScratchVolume, MountPath: "/scratch"}},
		TerminationMessagePath:   corev1.TerminationMessagePathDefault,
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
	}
}
