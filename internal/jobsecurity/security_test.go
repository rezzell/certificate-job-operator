package jobsecurity

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestBoolPointerTrue(t *testing.T) {
	t.Parallel()

	if BoolPointerTrue(nil) {
		t.Fatalf("nil should return false")
	}
	if BoolPointerTrue(boolPtr(false)) {
		t.Fatalf("false should return false")
	}
	if !BoolPointerTrue(boolPtr(true)) {
		t.Fatalf("true should return true")
	}
}

func TestContainsCapability(t *testing.T) {
	t.Parallel()

	if !ContainsCapability([]corev1.Capability{"NET_ADMIN", "ALL"}) {
		t.Fatalf("expected ALL to be found")
	}
	if ContainsCapability([]corev1.Capability{"NET_ADMIN"}) {
		t.Fatalf("did not expect ALL to be found")
	}
}

func TestValidateReservedLabels(t *testing.T) {
	t.Parallel()

	if err := ValidateReservedLabels(map[string]string{"app.kubernetes.io/managed-by": "x"}); err == nil {
		t.Fatalf("expected reserved label validation error")
	}
	if err := ValidateReservedLabels(map[string]string{"custom": "ok"}); err != nil {
		t.Fatalf("expected non-reserved labels to pass, got %v", err)
	}
}

func TestValidateJobTemplateSecurity(t *testing.T) {
	t.Parallel()

	t.Run("accepts minimal valid template", func(t *testing.T) {
		t.Parallel()

		spec := &batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "runner",
						Image: "busybox:1.36",
					}},
				},
			},
		}
		if err := ValidateJobTemplateSecurity(spec); err != nil {
			t.Fatalf("expected valid template, got %v", err)
		}
	})

	t.Run("rejects host network", func(t *testing.T) {
		t.Parallel()

		spec := &batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					HostNetwork: true,
					Containers: []corev1.Container{{
						Name:  "runner",
						Image: "busybox:1.36",
					}},
				},
			},
		}
		if err := ValidateJobTemplateSecurity(spec); err == nil {
			t.Fatalf("expected hostNetwork validation error")
		}
	})

	t.Run("rejects automount service account token true", func(t *testing.T) {
		t.Parallel()

		spec := &batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: boolPtr(true),
					Containers: []corev1.Container{{
						Name:  "runner",
						Image: "busybox:1.36",
					}},
				},
			},
		}
		if err := ValidateJobTemplateSecurity(spec); err == nil {
			t.Fatalf("expected automountServiceAccountToken validation error")
		}
	})

	t.Run("rejects projected service account token volume", func(t *testing.T) {
		t.Parallel()

		spec := &batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{{
						Name: "token",
						VolumeSource: corev1.VolumeSource{
							Projected: &corev1.ProjectedVolumeSource{
								Sources: []corev1.VolumeProjection{{
									ServiceAccountToken: &corev1.ServiceAccountTokenProjection{},
								}},
							},
						},
					}},
					Containers: []corev1.Container{{
						Name:  "runner",
						Image: "busybox:1.36",
					}},
				},
			},
		}
		if err := ValidateJobTemplateSecurity(spec); err == nil {
			t.Fatalf("expected projected service account token validation error")
		}
	})

	t.Run("rejects pod runAsNonRoot false", func(t *testing.T) {
		t.Parallel()

		spec := &batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: boolPtr(false)},
					Containers: []corev1.Container{{
						Name:  "runner",
						Image: "busybox:1.36",
					}},
				},
			},
		}
		if err := ValidateJobTemplateSecurity(spec); err == nil {
			t.Fatalf("expected pod runAsNonRoot validation error")
		}
	})

	t.Run("rejects container runAsNonRoot false", func(t *testing.T) {
		t.Parallel()

		spec := &batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "runner",
						Image: "busybox:1.36",
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot: boolPtr(false),
						},
					}},
				},
			},
		}
		if err := ValidateJobTemplateSecurity(spec); err == nil {
			t.Fatalf("expected container runAsNonRoot validation error")
		}
	})
}

func TestApplyJobSecurityDefaults(t *testing.T) {
	t.Parallel()

	t.Run("initializes empty security context", func(t *testing.T) {
		t.Parallel()

		spec := &batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "runner",
						Image: "busybox:1.36",
					}},
				},
			},
		}

		ApplyJobSecurityDefaults(spec, 3600)

		if spec.TTLSecondsAfterFinished == nil || *spec.TTLSecondsAfterFinished != 3600 {
			t.Fatalf("expected default TTL to be set, got %v", spec.TTLSecondsAfterFinished)
		}
		if spec.Template.Spec.AutomountServiceAccountToken == nil || *spec.Template.Spec.AutomountServiceAccountToken {
			t.Fatalf("expected automount service account token disabled")
		}
		if spec.Template.Spec.EnableServiceLinks == nil || *spec.Template.Spec.EnableServiceLinks {
			t.Fatalf("expected service links disabled")
		}
		if spec.Template.Spec.SecurityContext == nil || spec.Template.Spec.SecurityContext.SeccompProfile == nil {
			t.Fatalf("expected pod seccomp profile to be set")
		}
		if spec.Template.Spec.SecurityContext.RunAsNonRoot == nil || !*spec.Template.Spec.SecurityContext.RunAsNonRoot {
			t.Fatalf("expected pod runAsNonRoot=true")
		}

		container := spec.Template.Spec.Containers[0]
		if container.SecurityContext == nil {
			t.Fatalf("expected container security context to be initialized")
		}
		if container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation {
			t.Fatalf("expected privilege escalation to be disabled")
		}
		if container.SecurityContext.SeccompProfile == nil {
			t.Fatalf("expected container seccomp profile to be set")
		}
		if container.SecurityContext.RunAsNonRoot == nil || !*container.SecurityContext.RunAsNonRoot {
			t.Fatalf("expected container runAsNonRoot=true")
		}
		if container.SecurityContext.Capabilities == nil || !ContainsCapability(container.SecurityContext.Capabilities.Drop) {
			t.Fatalf("expected ALL capability to be dropped")
		}
	})

	t.Run("adds ALL drop capability when missing", func(t *testing.T) {
		t.Parallel()

		spec := &batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "runner",
						Image: "busybox:1.36",
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"NET_ADMIN"},
							},
						},
					}},
				},
			},
		}

		ApplyJobSecurityDefaults(spec, 3600)

		if !ContainsCapability(spec.Template.Spec.Containers[0].SecurityContext.Capabilities.Drop) {
			t.Fatalf("expected ALL capability to be present: %v", spec.Template.Spec.Containers[0].SecurityContext.Capabilities.Drop)
		}
	})
}

func boolPtr(v bool) *bool {
	return &v
}
