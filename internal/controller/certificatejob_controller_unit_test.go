package controller

import (
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	certificatesv1alpha1 "github.com/russell/certificate-job-operator/api/v1alpha1"
)

func TestBuildWorkflowGraph(t *testing.T) {
	t.Parallel()

	t.Run("builds dependency graph", func(t *testing.T) {
		t.Parallel()

		cjob := validCertificateJob(t)
		deps, reverse, err := buildWorkflowGraph(cjob)
		if err != nil {
			t.Fatalf("buildWorkflowGraph returned error: %v", err)
		}

		if got := deps["build"]; len(got) != 0 {
			t.Fatalf("unexpected deps for build: %v", got)
		}
		if got := deps["test"]; len(got) != 1 || got[0] != "build" {
			t.Fatalf("unexpected deps for test: %v", got)
		}
		if got := reverse["build"]; len(got) != 1 || got[0] != "test" {
			t.Fatalf("unexpected reverse deps for build: %v", got)
		}
	})

	t.Run("rejects duplicate names", func(t *testing.T) {
		t.Parallel()

		cjob := validCertificateJob(t)
		cjob.Spec.Jobs = append(cjob.Spec.Jobs, cjob.Spec.Jobs[0])

		_, _, err := buildWorkflowGraph(cjob)
		if err == nil || !strings.Contains(err.Error(), "duplicate job template name") {
			t.Fatalf("expected duplicate-name error, got %v", err)
		}
	})

	t.Run("rejects invalid edges", func(t *testing.T) {
		t.Parallel()

		cjob := validCertificateJob(t)
		cjob.Spec.Workflow.Edges = []certificatesv1alpha1.CertificateWorkflowEdge{{From: "build", To: "missing"}}

		_, _, err := buildWorkflowGraph(cjob)
		if err == nil || !strings.Contains(err.Error(), "both nodes must exist") {
			t.Fatalf("expected invalid-edge error, got %v", err)
		}
	})

	t.Run("rejects cycles", func(t *testing.T) {
		t.Parallel()

		cjob := validCertificateJob(t)
		cjob.Spec.Workflow.Edges = []certificatesv1alpha1.CertificateWorkflowEdge{
			{From: "build", To: "test"},
			{From: "test", To: "build"},
		}

		_, _, err := buildWorkflowGraph(cjob)
		if err == nil || !strings.Contains(err.Error(), "workflow edges contain a cycle") {
			t.Fatalf("expected cycle error, got %v", err)
		}
	})
}

func TestValidateAcyclic(t *testing.T) {
	t.Parallel()

	if err := validateAcyclic(map[string][]string{
		"build": nil,
		"test":  {"build"},
	}); err != nil {
		t.Fatalf("expected DAG to pass validation, got %v", err)
	}

	err := validateAcyclic(map[string][]string{
		"build": {"test"},
		"test":  {"build"},
	})
	if err == nil || !strings.Contains(err.Error(), "workflow edges contain a cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestDeriveWorkflowPhase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		nodes []certificatesv1alpha1.WorkflowNodeState
		want  certificatesv1alpha1.ExecutionPhase
	}{
		{
			name:  "failed dominates",
			nodes: []certificatesv1alpha1.WorkflowNodeState{{Phase: certificatesv1alpha1.ExecutionPhaseSucceeded}, {Phase: certificatesv1alpha1.ExecutionPhaseFailed}},
			want:  certificatesv1alpha1.ExecutionPhaseFailed,
		},
		{
			name:  "running dominates pending",
			nodes: []certificatesv1alpha1.WorkflowNodeState{{Phase: certificatesv1alpha1.ExecutionPhasePending}, {Phase: certificatesv1alpha1.ExecutionPhaseRunning}},
			want:  certificatesv1alpha1.ExecutionPhaseRunning,
		},
		{
			name:  "pending dominates terminal",
			nodes: []certificatesv1alpha1.WorkflowNodeState{{Phase: certificatesv1alpha1.ExecutionPhaseSkipped}, {Phase: certificatesv1alpha1.ExecutionPhasePending}},
			want:  certificatesv1alpha1.ExecutionPhasePending,
		},
		{
			name:  "all terminal means succeeded",
			nodes: []certificatesv1alpha1.WorkflowNodeState{{Phase: certificatesv1alpha1.ExecutionPhaseSucceeded}, {Phase: certificatesv1alpha1.ExecutionPhaseSkipped}},
			want:  certificatesv1alpha1.ExecutionPhaseSucceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := deriveWorkflowPhase(&certificatesv1alpha1.CertificateExecutionState{Nodes: tt.nodes})
			if got != tt.want {
				t.Fatalf("deriveWorkflowPhase() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildJobName(t *testing.T) {
	t.Parallel()

	if got := buildJobName("Job", "Cert", "Node", "Run-1"); got != "job-cert-node-run-1" {
		t.Fatalf("unexpected job name: %q", got)
	}

	long := buildJobName(strings.Repeat("a", 40), strings.Repeat("b", 40), "node", "run")
	if len(long) > 63 {
		t.Fatalf("job name should be truncated to 63 chars, got %d", len(long))
	}
	if strings.HasPrefix(long, "-") || strings.HasSuffix(long, "-") {
		t.Fatalf("job name should be trimmed, got %q", long)
	}
}

func TestSanitizeDNS1123(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"ABC_123": "abc-123",
		"---":     "job",
		"A..B":    "a--b",
	}

	for in, want := range tests {
		if got := sanitizeDNS1123(in); got != want {
			t.Fatalf("sanitizeDNS1123(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMergeMaps(t *testing.T) {
	t.Parallel()

	if got := mergeMaps(nil, nil); got != nil {
		t.Fatalf("expected nil map, got %v", got)
	}

	got := mergeMaps(map[string]string{"a": "1", "shared": "old"}, map[string]string{"b": "2", "shared": "new"})
	if len(got) != 3 {
		t.Fatalf("unexpected map length: %v", got)
	}
	if got["shared"] != "new" {
		t.Fatalf("expected extras to win, got %q", got["shared"])
	}
}

func TestJobStateHelpers(t *testing.T) {
	t.Parallel()

	job := &batchv1.Job{}
	if isJobComplete(job) || isJobFailed(job) {
		t.Fatalf("empty job should not be terminal")
	}

	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	if !isJobComplete(job) {
		t.Fatalf("expected job to be complete")
	}

	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}
	if !isJobFailed(job) {
		t.Fatalf("expected job to be failed")
	}

	if !isTerminalPhase(certificatesv1alpha1.ExecutionPhaseSucceeded) {
		t.Fatalf("succeeded should be terminal")
	}
	if isTerminalPhase(certificatesv1alpha1.ExecutionPhaseRunning) {
		t.Fatalf("running should not be terminal")
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
			t.Fatalf("expected ALL capability to be dropped")
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
		t.Fatalf("nil should return false")
	}
	if boolPointerTrue(boolPtr(false)) {
		t.Fatalf("false should return false")
	}
	if !boolPointerTrue(boolPtr(true)) {
		t.Fatalf("true should return true")
	}
}

func boolPtr(v bool) *bool {
	return &v
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
		if err := validateJobTemplateSecurity(spec); err != nil {
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
		if err := validateJobTemplateSecurity(spec); err == nil {
			t.Fatalf("expected hostNetwork validation error")
		}
	})
}

func TestHardenJobTemplate(t *testing.T) {
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

	hardenJobTemplate(spec, 3600)

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

	container := spec.Template.Spec.Containers[0]
	if container.SecurityContext == nil || container.SecurityContext.SeccompProfile == nil {
		t.Fatalf("expected container seccomp profile to be set")
	}
}

func TestInjectCertificateSecret(t *testing.T) {
	t.Parallel()

	spec := &batchv1.JobSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				InitContainers: []corev1.Container{{
					Name:  "init",
					Image: "busybox:1.36",
				}},
				Containers: []corev1.Container{{
					Name:  "runner",
					Image: "busybox:1.36",
				}},
			},
		},
	}

	injectCertificateSecret(spec, "tls-secret")

	if len(spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(spec.Template.Spec.Volumes))
	}
	if spec.Template.Spec.Volumes[0].Secret == nil || spec.Template.Spec.Volumes[0].Secret.SecretName != "tls-secret" {
		t.Fatalf("expected secret volume to reference tls-secret")
	}
	if len(spec.Template.Spec.Containers[0].VolumeMounts) != 1 {
		t.Fatalf("expected secret mount on main container")
	}
	if spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath != secretMountPath {
		t.Fatalf("expected mount path %q, got %q", secretMountPath, spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath)
	}

	injectCertificateSecret(spec, "tls-secret-updated")
	if spec.Template.Spec.Volumes[0].Secret == nil || spec.Template.Spec.Volumes[0].Secret.SecretName != "tls-secret-updated" {
		t.Fatalf("expected existing secret volume to be updated")
	}
}

func validCertificateJob(t *testing.T) *certificatesv1alpha1.CertificateJob {
	t.Helper()

	return &certificatesv1alpha1.CertificateJob{
		Spec: certificatesv1alpha1.CertificateJobSpec{
			Jobs: []certificatesv1alpha1.CertificateJobTemplate{
				{
					Name: "build",
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
				{
					Name: "test",
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
			Workflow: certificatesv1alpha1.CertificateWorkflowSpec{
				Edges: []certificatesv1alpha1.CertificateWorkflowEdge{{From: "build", To: "test"}},
			},
		},
	}
}
