package v1alpha1

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	certificatesv1alpha1 "github.com/russell/certificate-job-operator/api/v1alpha1"
)

func TestValidateCertificateJob(t *testing.T) {
	t.Parallel()

	t.Run("rejects empty job list", func(t *testing.T) {
		t.Parallel()

		err := validateCertificateJob(&certificatesv1alpha1.CertificateJob{})
		if err == nil || !strings.Contains(err.Error(), "spec.jobs must contain at least one template") {
			t.Fatalf("expected empty-job-list error, got %v", err)
		}
	})

	t.Run("rejects invalid job template security", func(t *testing.T) {
		t.Parallel()

		cjob := &certificatesv1alpha1.CertificateJob{
			Spec: certificatesv1alpha1.CertificateJobSpec{
				Jobs: []certificatesv1alpha1.CertificateJobTemplate{
					{
						Name: "job-a",
						Template: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									HostNetwork: true,
								},
							},
						},
					},
				},
			},
		}

		err := validateCertificateJob(cjob)
		if err == nil || !strings.Contains(err.Error(), "job \"job-a\" violates security policy") {
			t.Fatalf("expected security policy error, got %v", err)
		}
	})

	t.Run("rejects reserved template labels", func(t *testing.T) {
		t.Parallel()

		cjob := &certificatesv1alpha1.CertificateJob{
			Spec: certificatesv1alpha1.CertificateJobSpec{
				Jobs: []certificatesv1alpha1.CertificateJobTemplate{
					{
						Name:   "job-a",
						Labels: map[string]string{"app.kubernetes.io/managed-by": "user"},
						Template: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{{Name: "runner", Image: "busybox:1.36"}},
								},
							},
						},
					},
				},
			},
		}
		err := validateCertificateJob(cjob)
		if err == nil || !strings.Contains(err.Error(), "reserved by the operator") {
			t.Fatalf("expected reserved label error, got %v", err)
		}
	})

	t.Run("accepts a valid job", func(t *testing.T) {
		t.Parallel()

		cjob := &certificatesv1alpha1.CertificateJob{
			Spec: certificatesv1alpha1.CertificateJobSpec{
				Parallelism: int32Ptr(1),
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

		if err := validateCertificateJob(cjob); err != nil {
			t.Fatalf("expected valid job to pass validation, got %v", err)
		}
	})
}

func TestDefaultAppliesSecurityPolicy(t *testing.T) {
	t.Parallel()

	defaulter := CertificateJobCustomDefaulter{}
	cjob := &certificatesv1alpha1.CertificateJob{
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

	if err := defaulter.Default(context.Background(), cjob); err != nil {
		t.Fatalf("expected defaulting to succeed, got %v", err)
	}

	if cjob.Spec.JobTTLSecondsAfterFinished == nil || *cjob.Spec.JobTTLSecondsAfterFinished != 3600 {
		t.Fatalf("expected top-level default TTL to be set, got %v", cjob.Spec.JobTTLSecondsAfterFinished)
	}
	jobSpec := cjob.Spec.Jobs[0].Template
	if jobSpec.TTLSecondsAfterFinished == nil || *jobSpec.TTLSecondsAfterFinished != 3600 {
		t.Fatalf("expected job default TTL to be set, got %v", jobSpec.TTLSecondsAfterFinished)
	}
	if jobSpec.Template.Spec.AutomountServiceAccountToken == nil || *jobSpec.Template.Spec.AutomountServiceAccountToken {
		t.Fatalf("expected automount service account token to be disabled")
	}
	if jobSpec.Template.Spec.EnableServiceLinks == nil || *jobSpec.Template.Spec.EnableServiceLinks {
		t.Fatalf("expected service links to be disabled")
	}
	if jobSpec.Template.Spec.SecurityContext == nil || jobSpec.Template.Spec.SecurityContext.SeccompProfile == nil {
		t.Fatalf("expected pod seccomp profile to be set")
	}
	if jobSpec.Template.Spec.SecurityContext.RunAsNonRoot == nil || !*jobSpec.Template.Spec.SecurityContext.RunAsNonRoot {
		t.Fatalf("expected pod runAsNonRoot=true")
	}
	container := jobSpec.Template.Spec.Containers[0]
	if container.SecurityContext == nil || container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("expected container privilege escalation to be disabled")
	}
	if container.SecurityContext.SeccompProfile == nil {
		t.Fatalf("expected container seccomp profile to be set")
	}
	if container.SecurityContext.RunAsNonRoot == nil || !*container.SecurityContext.RunAsNonRoot {
		t.Fatalf("expected container runAsNonRoot=true")
	}
	if container.SecurityContext.Capabilities == nil || len(container.SecurityContext.Capabilities.Drop) == 0 {
		t.Fatalf("expected container capabilities drop list to be set")
	}
}

func int32Ptr(v int32) *int32 { return &v }

func TestValidateUpdateAndDelete(t *testing.T) {
	t.Parallel()

	validator := CertificateJobCustomValidator{}

	cjob := &certificatesv1alpha1.CertificateJob{
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

	if _, err := validator.ValidateUpdate(context.Background(), cjob, cjob); err != nil {
		t.Fatalf("expected update validation to pass, got %v", err)
	}

	if _, err := validator.ValidateDelete(context.Background(), cjob); err != nil {
		t.Fatalf("expected delete validation to pass, got %v", err)
	}
}
