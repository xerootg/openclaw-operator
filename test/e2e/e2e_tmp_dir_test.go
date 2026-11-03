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

package e2e

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	openclawv1alpha1 "github.com/paperclipinc/openclaw-operator/api/v1alpha1"
	"github.com/paperclipinc/openclaw-operator/internal/resources"
)

var _ = Describe("Safe temporary directories", func() {
	var namespace string

	BeforeEach(func() {
		if os.Getenv("E2E_SKIP_RESOURCE_VALIDATION") == "true" {
			Skip("Skipping resource validation in minimal mode")
		}
		namespace = "test-tmp-dir-" + time.Now().Format("20060102150405")
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).Should(Succeed())
	})

	AfterEach(func() {
		if namespace != "" {
			_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		}
	})

	It("Should mount prepared subdirectories instead of fsGroup-managed volume roots", func() {
		instance := &openclawv1alpha1.OpenClawInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name: "safe-tmp", Namespace: namespace,
				Annotations: map[string]string{"openclaw.rocks/skip-backup": "true"},
			},
			Spec: openclawv1alpha1.OpenClawInstanceSpec{
				Image:   openclawv1alpha1.ImageSpec{Repository: "ghcr.io/openclaw/openclaw", Tag: "2026.10.9"},
				Plugins: []string{"@openclaw/duckduckgo-plugin@2026.10.9"},
			},
		}
		Expect(k8sClient.Create(ctx, instance)).Should(Succeed())
		sts := &appsv1.StatefulSet{}
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name: resources.StatefulSetName(instance), Namespace: namespace,
			}, sts)
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		var scratch, installer, tmp *corev1.Container
		for i := range sts.Spec.Template.Spec.InitContainers {
			c := &sts.Spec.Template.Spec.InitContainers[i]
			switch c.Name {
			case "init-plugin-scratch":
				scratch = c
			case "init-plugins":
				installer = c
				Expect(i).To(BeNumerically(">", 0))
				Expect(sts.Spec.Template.Spec.InitContainers[i-1].Name).To(Equal("init-plugin-scratch"))
			case resources.TmpDirInitContainerName:
				tmp = c
			}
		}
		Expect(scratch).NotTo(BeNil())
		Expect(installer).NotTo(BeNil())
		Expect(tmp).NotTo(BeNil())
		Expect(scratch.Command[2]).To(ContainSubstring("fs.chmodSync('/scratch/private', 0o700)"))
		Expect(scratch.VolumeMounts).To(ConsistOf(corev1.VolumeMount{Name: "plugins-tmp", MountPath: "/scratch"}))
		Expect(installer.VolumeMounts).To(ContainElement(corev1.VolumeMount{Name: "plugins-tmp", MountPath: "/tmp", SubPath: "private"}))
		Expect(tmp.Command[2]).To(ContainSubstring("chmod 01777 /tmp-volume/" + resources.MainTmpSubPath))
		Expect(tmp.VolumeMounts).To(ConsistOf(corev1.VolumeMount{Name: "tmp", MountPath: "/tmp-volume"}))
		Expect(sts.Spec.Template.Spec.Containers[0].VolumeMounts).To(ContainElement(corev1.VolumeMount{
			Name: "tmp", MountPath: "/tmp", SubPath: resources.MainTmpSubPath,
		}))
	})
})
