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

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	certificatesv1alpha1 "github.com/russell/certificate-job-operator/api/v1alpha1"
	"github.com/russell/certificate-job-operator/internal/jobsecurity"
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
		jobsecurity.ApplyJobSecurityDefaults(jobSpec, *certificateJob.Spec.JobTTLSecondsAfterFinished)
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
		if err := jobsecurity.ValidateReservedLabels(job.Labels); err != nil {
			return fmt.Errorf("job %q has invalid labels: %w", job.Name, err)
		}
		if err := jobsecurity.ValidateReservedLabels(job.Template.Template.Labels); err != nil {
			return fmt.Errorf("job %q has invalid pod template labels: %w", job.Name, err)
		}
		if err := jobsecurity.ValidateJobTemplateSecurity(&job.Template); err != nil {
			return fmt.Errorf("job %q violates security policy: %w", job.Name, err)
		}
	}

	return nil
}
