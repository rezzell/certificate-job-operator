//go:build integration
// +build integration

/*
Copyright 2026.

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

package v1alpha1

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	certificatesv1alpha1 "github.com/russell/certificate-job-operator/api/v1alpha1"
)

var _ = Describe("CertificateJob Webhook", func() {
	newValidCertificateJob := func() *certificatesv1alpha1.CertificateJob {
		return &certificatesv1alpha1.CertificateJob{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "cjob-webhook-",
				Namespace:    "default",
			},
			Spec: certificatesv1alpha1.CertificateJobSpec{
				Jobs: []certificatesv1alpha1.CertificateJobTemplate{
					{
						Name: "job-a",
						Template: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{{
										Name:  "runner",
										Image: "busybox:1.36",
									}},
								},
							},
						},
					},
				},
			},
		}
	}

	It("admits a valid CertificateJob", func() {
		obj := newValidCertificateJob()

		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
	})

	It("rejects invalid security settings", func() {
		obj := newValidCertificateJob()
		obj.Spec.Jobs[0].Template.Template.Spec.HostNetwork = true

		err := k8sClient.Create(ctx, obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("violates security policy"))
		Expect(err.Error()).To(ContainSubstring("hostNetwork is not allowed"))
	})

	It("defaults job security settings during admission", func() {
		obj := newValidCertificateJob()

		Expect(k8sClient.Create(ctx, obj)).To(Succeed())

		Expect(obj.Spec.JobTTLSecondsAfterFinished).NotTo(BeNil())
		Expect(*obj.Spec.JobTTLSecondsAfterFinished).To(Equal(int32(3600)))
		jobSpec := obj.Spec.Jobs[0].Template
		Expect(jobSpec.TTLSecondsAfterFinished).NotTo(BeNil())
		Expect(*jobSpec.TTLSecondsAfterFinished).To(Equal(int32(3600)))
		Expect(jobSpec.Template.Spec.AutomountServiceAccountToken).NotTo(BeNil())
		Expect(*jobSpec.Template.Spec.AutomountServiceAccountToken).To(BeFalse())
	})
})
