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

func TestApplyJobSecurityDefaults(t *testing.T) {
	t.Parallel()

	jobSpec := &batchv1.JobSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "runner",
					Image: "busybox:1.36",
				}},
			},
		},
	}

	applyJobSecurityDefaults(jobSpec, 3600)

	if jobSpec.TTLSecondsAfterFinished == nil || *jobSpec.TTLSecondsAfterFinished != 3600 {
		t.Fatalf("expected default TTL to be set, got %v", jobSpec.TTLSecondsAfterFinished)
	}

	if jobSpec.Template.Spec.AutomountServiceAccountToken == nil || *jobSpec.Template.Spec.AutomountServiceAccountToken {
		t.Fatalf("expected automount service account token to be disabled")
	}

	if jobSpec.Template.Spec.EnableServiceLinks == nil || *jobSpec.Template.Spec.EnableServiceLinks {
		t.Fatalf("expected enable service links to be disabled")
	}

	if jobSpec.Template.Spec.SecurityContext == nil || jobSpec.Template.Spec.SecurityContext.SeccompProfile == nil {
		t.Fatalf("expected pod seccomp profile to be set")
	}

	container := jobSpec.Template.Spec.Containers[0]
	if container.SecurityContext == nil || container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("expected container privilege escalation to be disabled")
	}
	if container.SecurityContext.SeccompProfile == nil {
		t.Fatalf("expected container seccomp profile to be set")
	}
	if container.SecurityContext.Capabilities == nil || !containsCapability(container.SecurityContext.Capabilities.Drop) {
		t.Fatalf("expected ALL to be dropped")
	}
}

func TestApplyContainerSecurityDefaults(t *testing.T) {
	t.Parallel()

	t.Run("initializes empty security context", func(t *testing.T) {
		t.Parallel()

		container := &corev1.Container{Name: "runner"}
		applyContainerSecurityDefaults(container)

		if container.SecurityContext == nil {
			t.Fatalf("expected security context to be initialized")
		}
		if container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation {
			t.Fatalf("expected privilege escalation to be disabled")
		}
		if container.SecurityContext.SeccompProfile == nil {
			t.Fatalf("expected seccomp profile to be initialized")
		}
		if container.SecurityContext.Capabilities == nil || !containsCapability(container.SecurityContext.Capabilities.Drop) {
			t.Fatalf("expected ALL to be dropped")
		}
	})

	t.Run("adds ALL drop capability when missing", func(t *testing.T) {
		t.Parallel()

		container := &corev1.Container{
			Name: "runner",
			SecurityContext: &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"NET_ADMIN"},
				},
			},
		}

		applyContainerSecurityDefaults(container)

		if !containsCapability(container.SecurityContext.Capabilities.Drop) {
			t.Fatalf("expected ALL capability to be present: %v", container.SecurityContext.Capabilities.Drop)
		}
	})
}

func TestBoolPointerTrue(t *testing.T) {
	t.Parallel()

	if boolPointerTrue(nil) {
		t.Fatalf("nil should be false")
	}

	if boolPointerTrue(boolPtr(false)) {
		t.Fatalf("false should be false")
	}

	if !boolPointerTrue(boolPtr(true)) {
		t.Fatalf("true should be true")
	}
}

func TestContainsCapability(t *testing.T) {
	t.Parallel()

	if !containsCapability([]corev1.Capability{"NET_ADMIN", "ALL"}) {
		t.Fatalf("expected ALL to be found")
	}

	if containsCapability([]corev1.Capability{"NET_ADMIN"}) {
		t.Fatalf("did not expect ALL to be found")
	}
}

func int32Ptr(v int32) *int32 {
	return &v
}

func boolPtr(v bool) *bool {
	return &v
}

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
