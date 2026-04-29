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
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	certificatesv1alpha1 "github.com/russell/certificate-job-operator/api/v1alpha1"
)

var certificatejoblog = logf.Log.WithName("certificatejob-resource")

func SetupCertificateJobWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &certificatesv1alpha1.CertificateJob{}).
		WithValidator(&CertificateJobCustomValidator{}).
		WithDefaulter(&CertificateJobCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-certificates-rezzell-com-v1alpha1-certificatejob,mutating=true,failurePolicy=fail,sideEffects=None,groups=certificates.rezzell.com,resources=certificatejobs,verbs=create;update,versions=v1alpha1,name=mcertificatejob-v1alpha1.kb.io,admissionReviewVersions=v1

type CertificateJobCustomDefaulter struct{}

var _ admission.Defaulter[*certificatesv1alpha1.CertificateJob] = &CertificateJobCustomDefaulter{}

func (d *CertificateJobCustomDefaulter) Default(_ context.Context, certificateJob *certificatesv1alpha1.CertificateJob) error {
	certificatejoblog.Info("defaulting CertificateJob", "namespace", certificateJob.Namespace, "name", certificateJob.Name)

	if certificateJob.Spec.Parallelism == nil {
		parallelism := int32(1)
		certificateJob.Spec.Parallelism = &parallelism
	}
	if certificateJob.Spec.JobTTLSecondsAfterFinished == nil {
		ttl := int32(3600)
		certificateJob.Spec.JobTTLSecondsAfterFinished = &ttl
	}
	if certificateJob.Spec.FailurePolicy == "" {
		certificateJob.Spec.FailurePolicy = certificatesv1alpha1.FailurePolicyStopDownstream
	}

	for i := range certificateJob.Spec.Jobs {
		jobSpec := &certificateJob.Spec.Jobs[i].Template
		applyJobSecurityDefaults(jobSpec, *certificateJob.Spec.JobTTLSecondsAfterFinished)
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-certificates-rezzell-com-v1alpha1-certificatejob,mutating=false,failurePolicy=fail,sideEffects=None,groups=certificates.rezzell.com,resources=certificatejobs,verbs=create;update,versions=v1alpha1,name=vcertificatejob-v1alpha1.kb.io,admissionReviewVersions=v1

type CertificateJobCustomValidator struct{}

var _ admission.Validator[*certificatesv1alpha1.CertificateJob] = &CertificateJobCustomValidator{}

func (v *CertificateJobCustomValidator) ValidateCreate(_ context.Context, certificateJob *certificatesv1alpha1.CertificateJob) (admission.Warnings, error) {
	if err := validateCertificateJob(certificateJob); err != nil {
		return nil, err
	}
	return nil, nil
}

func (v *CertificateJobCustomValidator) ValidateUpdate(_ context.Context, _ *certificatesv1alpha1.CertificateJob, certificateJob *certificatesv1alpha1.CertificateJob) (admission.Warnings, error) {
	if err := validateCertificateJob(certificateJob); err != nil {
		return nil, err
	}
	return nil, nil
}

func (v *CertificateJobCustomValidator) ValidateDelete(_ context.Context, _ *certificatesv1alpha1.CertificateJob) (admission.Warnings, error) {
	return nil, nil
}

func validateCertificateJob(certificateJob *certificatesv1alpha1.CertificateJob) error {
	if len(certificateJob.Spec.Jobs) == 0 {
		return fmt.Errorf("spec.jobs must contain at least one template")
	}

	if certificateJob.Spec.Parallelism != nil && *certificateJob.Spec.Parallelism < 1 {
		return fmt.Errorf("spec.parallelism must be >= 1")
	}

	for _, job := range certificateJob.Spec.Jobs {
		if job.Name == "" {
			return fmt.Errorf("job template name cannot be empty")
		}
		if err := validateJobTemplateSecurity(&job.Template); err != nil {
			return fmt.Errorf("job %q violates security policy: %w", job.Name, err)
		}
	}

	return nil
}

func validateJobTemplateSecurity(spec *batchv1.JobSpec) error {
	podSpec := spec.Template.Spec

	if podSpec.HostNetwork {
		return fmt.Errorf("hostNetwork is not allowed")
	}
	if podSpec.HostPID {
		return fmt.Errorf("hostPID is not allowed")
	}
	if podSpec.HostIPC {
		return fmt.Errorf("hostIPC is not allowed")
	}
	if podSpec.ServiceAccountName != "" {
		return fmt.Errorf("serviceAccountName override is not allowed")
	}

	for _, volume := range podSpec.Volumes {
		if volume.HostPath != nil {
			return fmt.Errorf("hostPath volume %q is not allowed", volume.Name)
		}
	}

	containers := make([]corev1.Container, 0, len(podSpec.InitContainers)+len(podSpec.Containers))
	containers = append(containers, podSpec.InitContainers...)
	containers = append(containers, podSpec.Containers...)
	for _, container := range containers {
		if container.SecurityContext == nil {
			continue
		}
		if boolPointerTrue(container.SecurityContext.Privileged) {
			return fmt.Errorf("container %q cannot run privileged", container.Name)
		}
		if boolPointerTrue(container.SecurityContext.AllowPrivilegeEscalation) {
			return fmt.Errorf("container %q cannot allow privilege escalation", container.Name)
		}
		if container.SecurityContext.Capabilities != nil && len(container.SecurityContext.Capabilities.Add) > 0 {
			return fmt.Errorf("container %q cannot add linux capabilities", container.Name)
		}
		if container.SecurityContext.RunAsUser != nil && *container.SecurityContext.RunAsUser == 0 {
			return fmt.Errorf("container %q cannot run as root", container.Name)
		}
	}

	return nil
}

func applyJobSecurityDefaults(spec *batchv1.JobSpec, defaultTTL int32) {
	if spec.TTLSecondsAfterFinished == nil {
		ttl := defaultTTL
		spec.TTLSecondsAfterFinished = &ttl
	}

	podSpec := &spec.Template.Spec
	if podSpec.AutomountServiceAccountToken == nil {
		disabled := false
		podSpec.AutomountServiceAccountToken = &disabled
	}
	if podSpec.EnableServiceLinks == nil {
		disabled := false
		podSpec.EnableServiceLinks = &disabled
	}

	if podSpec.SecurityContext == nil {
		podSpec.SecurityContext = &corev1.PodSecurityContext{}
	}
	if podSpec.SecurityContext.SeccompProfile == nil {
		podSpec.SecurityContext.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
	}

	for i := range podSpec.InitContainers {
		applyContainerSecurityDefaults(&podSpec.InitContainers[i])
	}
	for i := range podSpec.Containers {
		applyContainerSecurityDefaults(&podSpec.Containers[i])
	}
}

func applyContainerSecurityDefaults(container *corev1.Container) {
	if container.SecurityContext == nil {
		container.SecurityContext = &corev1.SecurityContext{}
	}
	if container.SecurityContext.AllowPrivilegeEscalation == nil {
		disabled := false
		container.SecurityContext.AllowPrivilegeEscalation = &disabled
	}
	if container.SecurityContext.SeccompProfile == nil {
		container.SecurityContext.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
	}
	if container.SecurityContext.Capabilities == nil {
		container.SecurityContext.Capabilities = &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}
		return
	}
	if !containsCapability(container.SecurityContext.Capabilities.Drop) {
		container.SecurityContext.Capabilities.Drop = append(container.SecurityContext.Capabilities.Drop, "ALL")
	}
}

func boolPointerTrue(v *bool) bool {
	return v != nil && *v
}

func containsCapability(capabilities []corev1.Capability) bool {
	for _, capability := range capabilities {
		if capability == "ALL" {
			return true
		}
	}
	return false
}
