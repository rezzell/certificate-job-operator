package v1alpha1

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	certificatesv1alpha1 "github.com/russell/certificate-job-operator/api/v1alpha1"
)

func TestValidateCertificateJobAdditionalCases(t *testing.T) {
	t.Parallel()

	t.Run("rejects parallelism less than one", func(t *testing.T) {
		t.Parallel()

		cjob := validWebhookCertificateJob()
		cjob.Spec.Parallelism = int32Ptr(0)

		err := validateCertificateJob(cjob)
		if err == nil || !strings.Contains(err.Error(), "spec.parallelism must be >= 1") {
			t.Fatalf("expected parallelism error, got %v", err)
		}
	})

	t.Run("rejects empty job template name", func(t *testing.T) {
		t.Parallel()

		cjob := validWebhookCertificateJob()
		cjob.Spec.Jobs[0].Name = ""

		err := validateCertificateJob(cjob)
		if err == nil || !strings.Contains(err.Error(), "job template name cannot be empty") {
			t.Fatalf("expected empty-name error, got %v", err)
		}
	})

	t.Run("rejects reserved pod template labels", func(t *testing.T) {
		t.Parallel()

		cjob := validWebhookCertificateJob()
		cjob.Spec.Jobs[0].Template.Template.Labels = map[string]string{"app.kubernetes.io/managed-by": "user"}

		err := validateCertificateJob(cjob)
		if err == nil || !strings.Contains(err.Error(), "invalid pod template labels") {
			t.Fatalf("expected reserved pod-template label error, got %v", err)
		}
	})
}

func TestValidateCreateUsesValidationRules(t *testing.T) {
	t.Parallel()

	validator := CertificateJobCustomValidator{}

	if _, err := validator.ValidateCreate(context.Background(), &certificatesv1alpha1.CertificateJob{}); err == nil {
		t.Fatalf("expected create validation to reject empty job list")
	}

	if _, err := validator.ValidateCreate(context.Background(), validWebhookCertificateJob()); err != nil {
		t.Fatalf("expected create validation to pass for a valid spec, got %v", err)
	}
}

func TestDefaultPreservesExplicitTopLevelValues(t *testing.T) {
	t.Parallel()

	parallelism := int32(5)
	ttl := int32(7200)
	jobTTL := int32(111)

	defaulter := CertificateJobCustomDefaulter{}
	cjob := validWebhookCertificateJob()
	cjob.Spec.Parallelism = &parallelism
	cjob.Spec.JobTTLSecondsAfterFinished = &ttl
	cjob.Spec.FailurePolicy = certificatesv1alpha1.FailurePolicyBestEffort
	cjob.Spec.Jobs[0].Template.TTLSecondsAfterFinished = &jobTTL

	if err := defaulter.Default(context.Background(), cjob); err != nil {
		t.Fatalf("expected defaulting to succeed, got %v", err)
	}

	if cjob.Spec.Parallelism == nil || *cjob.Spec.Parallelism != parallelism {
		t.Fatalf("expected explicit parallelism to be preserved, got %v", cjob.Spec.Parallelism)
	}
	if cjob.Spec.JobTTLSecondsAfterFinished == nil || *cjob.Spec.JobTTLSecondsAfterFinished != ttl {
		t.Fatalf("expected explicit top-level TTL to be preserved, got %v", cjob.Spec.JobTTLSecondsAfterFinished)
	}
	if cjob.Spec.FailurePolicy != certificatesv1alpha1.FailurePolicyBestEffort {
		t.Fatalf("expected explicit failure policy to be preserved, got %s", cjob.Spec.FailurePolicy)
	}
	if cjob.Spec.Jobs[0].Template.TTLSecondsAfterFinished == nil || *cjob.Spec.Jobs[0].Template.TTLSecondsAfterFinished != jobTTL {
		t.Fatalf("expected explicit job TTL to be preserved, got %v", cjob.Spec.Jobs[0].Template.TTLSecondsAfterFinished)
	}
}

func validWebhookCertificateJob() *certificatesv1alpha1.CertificateJob {
	return &certificatesv1alpha1.CertificateJob{
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "default"},
		Spec: certificatesv1alpha1.CertificateJobSpec{
			Jobs: []certificatesv1alpha1.CertificateJobTemplate{{
				Name: "job-a",
				Template: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "runner", Image: "busybox:1.36"}},
						},
					},
				},
			}},
		},
	}
}
