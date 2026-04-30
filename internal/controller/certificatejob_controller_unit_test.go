package controller

import (
	"strings"
	"testing"

	certmgrv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

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

func TestSelectorMatchesCertificateLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		selector   labels.Selector
		certLabels map[string]string
		want       bool
	}{
		{
			name:       "nil selector matches all",
			selector:   nil,
			certLabels: map[string]string{"team": "platform"},
			want:       true,
		},
		{
			name:       "empty selector matches nil labels",
			selector:   labels.Everything(),
			certLabels: nil,
			want:       true,
		},
		{
			name:       "requirement missing on nil labels does not match",
			selector:   labels.SelectorFromSet(map[string]string{"app": "web"}),
			certLabels: nil,
			want:       false,
		},
		{
			name:       "matching requirement with labels",
			selector:   labels.SelectorFromSet(map[string]string{"app": "web"}),
			certLabels: map[string]string{"app": "web"},
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := selectorMatchesCertificateLabels(tt.selector, tt.certLabels)
			if got != tt.want {
				t.Fatalf("selectorMatchesCertificateLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCertificateSelectorForJob(t *testing.T) {
	t.Parallel()

	cjob := validCertificateJob(t)
	cjob.Spec.CertificateSelector.MatchExpressions = []metav1.LabelSelectorRequirement{{
		Key:      "app",
		Operator: metav1.LabelSelectorOpIn,
	}}

	_, err := certificateSelectorForJob(cjob)
	if err == nil || !strings.Contains(err.Error(), "invalid certificateSelector") {
		t.Fatalf("expected invalid selector error, got %v", err)
	}
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

	got := buildJobName("Job", "Cert", "Node", "Run-1")
	if !strings.HasPrefix(got, "job-cert-node-run-1-") {
		t.Fatalf("unexpected job name prefix: %q", got)
	}

	long := buildJobName(strings.Repeat("a", 40), strings.Repeat("b", 40), "node", "run")
	if len(long) > 63 {
		t.Fatalf("job name should be truncated to 63 chars, got %d", len(long))
	}
	if strings.HasPrefix(long, "-") || strings.HasSuffix(long, "-") {
		t.Fatalf("job name should be trimmed, got %q", long)
	}

	c1 := buildJobName(strings.Repeat("a", 40), strings.Repeat("b", 40), "node", "run-1")
	c2 := buildJobName(strings.Repeat("a", 40), strings.Repeat("b", 40), "node", "run-2")
	if c1 == c2 {
		t.Fatalf("expected collision-safe names, got identical %q", c1)
	}
}

func TestBuildInputHashIncludesWorkflowSpecHash(t *testing.T) {
	t.Parallel()

	cert := &certmgrv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{Generation: 1},
		Spec: certmgrv1.CertificateSpec{
			SecretName: "tls-secret",
		},
	}
	secret := &corev1.Secret{
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{"tls.crt": []byte("crt"), "tls.key": []byte("key")},
	}

	h1, err := buildInputHash(cert, secret, "spec-a")
	if err != nil {
		t.Fatalf("buildInputHash returned error: %v", err)
	}
	h2, err := buildInputHash(cert, secret, "spec-b")
	if err != nil {
		t.Fatalf("buildInputHash returned error: %v", err)
	}
	if h1 == h2 {
		t.Fatalf("expected workflow spec hash to affect input hash")
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

func TestMergeMapsWithBasePrecedence(t *testing.T) {
	t.Parallel()

	got := mergeMapsWithBasePrecedence(
		map[string]string{"reserved": "base", "a": "1"},
		map[string]string{"reserved": "extra", "b": "2"},
	)
	if got["reserved"] != "base" {
		t.Fatalf("expected base to win for reserved key, got %q", got["reserved"])
	}
	if got["b"] != "2" {
		t.Fatalf("expected extra-only key to be preserved")
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
	if spec.Template.Spec.AutomountServiceAccountToken == nil || *spec.Template.Spec.AutomountServiceAccountToken {
		t.Fatalf("expected automount service account token to be forced disabled")
	}

	container := spec.Template.Spec.Containers[0]
	if container.SecurityContext == nil || container.SecurityContext.SeccompProfile == nil {
		t.Fatalf("expected container seccomp profile to be set")
	}
	if container.SecurityContext.RunAsNonRoot == nil || !*container.SecurityContext.RunAsNonRoot {
		t.Fatalf("expected container runAsNonRoot=true")
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
