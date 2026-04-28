package controller

import (
	"testing"

	certificatesv1alpha1 "github.com/russell/certificate-job-operator/api/v1alpha1"
)

func TestBuildWorkflowGraphDetectsCycle(t *testing.T) {
	cjob := &certificatesv1alpha1.CertificateJob{}
	cjob.Spec.Jobs = []certificatesv1alpha1.CertificateJobTemplate{
		{Name: "a"},
		{Name: "b"},
	}
	cjob.Spec.Workflow.Edges = []certificatesv1alpha1.CertificateWorkflowEdge{
		{From: "a", To: "b"},
		{From: "b", To: "a"},
	}

	if _, _, err := buildWorkflowGraph(cjob); err == nil {
		t.Fatalf("expected cycle validation error")
	}
}

func TestRunnableNodesWithDependencies(t *testing.T) {
	state := &certificatesv1alpha1.CertificateExecutionState{
		Nodes: []certificatesv1alpha1.WorkflowNodeState{
			{Name: "copy", Phase: certificatesv1alpha1.ExecutionPhaseSucceeded},
			{Name: "notify", Phase: certificatesv1alpha1.ExecutionPhasePending},
			{Name: "cleanup", Phase: certificatesv1alpha1.ExecutionPhasePending},
		},
	}

	deps := map[string][]string{
		"copy":    {},
		"notify":  {"copy"},
		"cleanup": {"notify"},
	}

	runnable := runnableNodes(certificatesv1alpha1.FailurePolicyStopDownstream, state, deps)
	if len(runnable) != 1 || runnable[0] != "notify" {
		t.Fatalf("expected only notify runnable, got %#v", runnable)
	}
}

func TestApplyFailurePolicyContinueIndependent(t *testing.T) {
	state := &certificatesv1alpha1.CertificateExecutionState{
		Nodes: []certificatesv1alpha1.WorkflowNodeState{
			{Name: "a", Phase: certificatesv1alpha1.ExecutionPhaseFailed},
			{Name: "b", Phase: certificatesv1alpha1.ExecutionPhasePending},
			{Name: "c", Phase: certificatesv1alpha1.ExecutionPhasePending},
			{Name: "d", Phase: certificatesv1alpha1.ExecutionPhasePending},
		},
	}
	deps := map[string][]string{
		"a": {},
		"b": {"a"},
		"c": {"a"},
		"d": {},
	}
	reverse := map[string][]string{
		"a": {"b", "c"},
		"b": {},
		"c": {},
		"d": {},
	}

	applyFailurePolicy(certificatesv1alpha1.FailurePolicyContinueIndependent, state, deps, reverse, []string{"a"})

	nodes := nodeStateMap(state)
	if nodes["b"].Phase != certificatesv1alpha1.ExecutionPhaseSkipped {
		t.Fatalf("expected b to be skipped")
	}
	if nodes["c"].Phase != certificatesv1alpha1.ExecutionPhaseSkipped {
		t.Fatalf("expected c to be skipped")
	}
	if nodes["d"].Phase != certificatesv1alpha1.ExecutionPhasePending {
		t.Fatalf("expected d to remain pending")
	}
}
