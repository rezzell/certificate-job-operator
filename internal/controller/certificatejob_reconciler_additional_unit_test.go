package controller

import (
	"context"
	"testing"

	certmgrv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	certificatesv1alpha1 "github.com/russell/certificate-job-operator/api/v1alpha1"
)

func TestObserveWorkflowNodeJobsUpdatesNodeStates(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	ns := "default"

	completeJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "job-complete", Namespace: ns},
		Status:     batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}},
	}
	failedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "job-failed", Namespace: ns},
		Status:     batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}},
	}
	activeJob := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "job-active", Namespace: ns}, Status: batchv1.JobStatus{Active: 1}}
	idleJob := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "job-idle", Namespace: ns}}

	r := &CertificateJobReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(completeJob, failedJob, activeJob, idleJob).Build(),
		Scheme: scheme,
	}

	state := &certificatesv1alpha1.CertificateExecutionState{Nodes: []certificatesv1alpha1.WorkflowNodeState{
		{Name: "complete", JobName: completeJob.Name, Phase: certificatesv1alpha1.ExecutionPhaseRunning},
		{Name: "failed", JobName: failedJob.Name, Phase: certificatesv1alpha1.ExecutionPhaseRunning},
		{Name: "active", JobName: activeJob.Name, Phase: certificatesv1alpha1.ExecutionPhasePending},
		{Name: "idle", JobName: idleJob.Name, Phase: certificatesv1alpha1.ExecutionPhasePending},
		{Name: "missing", JobName: "job-missing", Phase: certificatesv1alpha1.ExecutionPhaseRunning},
	}}

	now := metav1.Now()
	activeCount, failedNodes, err := r.observeWorkflowNodeJobs(context.Background(), &certmgrv1.Certificate{ObjectMeta: metav1.ObjectMeta{Namespace: ns}}, state, now)
	if err != nil {
		t.Fatalf("observeWorkflowNodeJobs returned error: %v", err)
	}
	if activeCount != 2 {
		t.Fatalf("expected 2 active nodes, got %d", activeCount)
	}
	if !failedNodes.Has("failed") || !failedNodes.Has("missing") || failedNodes.Len() != 2 {
		t.Fatalf("unexpected failed nodes: %v", failedNodes.UnsortedList())
	}

	nodes := nodeStateMap(state)
	if nodes["complete"].Phase != certificatesv1alpha1.ExecutionPhaseSucceeded {
		t.Fatalf("expected complete node to succeed, got %s", nodes["complete"].Phase)
	}
	if nodes["failed"].Phase != certificatesv1alpha1.ExecutionPhaseFailed {
		t.Fatalf("expected failed node to fail, got %s", nodes["failed"].Phase)
	}
	if nodes["active"].Phase != certificatesv1alpha1.ExecutionPhaseRunning {
		t.Fatalf("expected active node to be running, got %s", nodes["active"].Phase)
	}
	if nodes["idle"].Phase != certificatesv1alpha1.ExecutionPhaseRunning {
		t.Fatalf("expected idle node to be promoted to running, got %s", nodes["idle"].Phase)
	}
	if nodes["missing"].Phase != certificatesv1alpha1.ExecutionPhaseFailed {
		t.Fatalf("expected missing node to fail, got %s", nodes["missing"].Phase)
	}
}

func TestScheduleRunnableNodesCreatesJob(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	cjob := validCertificateJob(t)
	cjob.Name = "cjob"
	cjob.Namespace = "default"

	deps, _, err := buildWorkflowGraph(cjob)
	if err != nil {
		t.Fatalf("buildWorkflowGraph returned error: %v", err)
	}

	state := &certificatesv1alpha1.CertificateExecutionState{
		RunID: "run-123",
		Nodes: initializeNodeStates(cjob),
	}
	cert := &certmgrv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{Name: "cert-a", Namespace: "default"},
		Spec:       certmgrv1.CertificateSpec{SecretName: "tls-secret"},
	}

	r := &CertificateJobReconciler{
		Client:   fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(4),
	}

	now := metav1.Now()
	nodes := nodeStateMap(state)
	scheduled, err := r.scheduleRunnableNodes(context.Background(), cjob, cert, state, deps, nodes, 1, now)
	if err != nil {
		t.Fatalf("scheduleRunnableNodes returned error: %v", err)
	}
	if scheduled != 1 {
		t.Fatalf("expected one scheduled node, got %d", scheduled)
	}

	buildNode := nodes["build"]
	if buildNode.JobName == "" {
		t.Fatalf("expected build node job name to be set")
	}
	if buildNode.Phase != certificatesv1alpha1.ExecutionPhaseRunning {
		t.Fatalf("expected build node running, got %s", buildNode.Phase)
	}

	created := &batchv1.Job{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: buildNode.JobName, Namespace: "default"}, created); err != nil {
		t.Fatalf("expected created job to exist: %v", err)
	}
	if created.Labels["app.kubernetes.io/managed-by"] != "certificate-job-operator" {
		t.Fatalf("expected managed-by label to be enforced")
	}
	if created.Spec.TTLSecondsAfterFinished == nil || *created.Spec.TTLSecondsAfterFinished != 3600 {
		t.Fatalf("expected default TTL to be applied, got %v", created.Spec.TTLSecondsAfterFinished)
	}
	if len(created.Spec.Template.Spec.Volumes) == 0 || created.Spec.Template.Spec.Volumes[0].Secret == nil || created.Spec.Template.Spec.Volumes[0].Secret.SecretName != "tls-secret" {
		t.Fatalf("expected certificate secret volume to be injected")
	}
	if len(created.Spec.Template.Spec.Containers) == 0 || len(created.Spec.Template.Spec.Containers[0].VolumeMounts) == 0 {
		t.Fatalf("expected volume mount to be injected into container")
	}
}

func TestMapCertificateToCertificateJobsFiltersSelectors(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	ns := "default"

	matching := &certificatesv1alpha1.CertificateJob{
		ObjectMeta: metav1.ObjectMeta{Name: "match", Namespace: ns},
		Spec:       certificatesv1alpha1.CertificateJobSpec{CertificateSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
	}
	nonMatching := &certificatesv1alpha1.CertificateJob{
		ObjectMeta: metav1.ObjectMeta{Name: "miss", Namespace: ns},
		Spec:       certificatesv1alpha1.CertificateJobSpec{CertificateSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}},
	}
	invalid := &certificatesv1alpha1.CertificateJob{
		ObjectMeta: metav1.ObjectMeta{Name: "invalid", Namespace: ns},
		Spec:       certificatesv1alpha1.CertificateJobSpec{CertificateSelector: metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: metav1.LabelSelectorOpIn}}}},
	}

	r := &CertificateJobReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(matching, nonMatching, invalid).Build(), Scheme: scheme}

	requests := r.mapCertificateToCertificateJobs(context.Background(), &certmgrv1.Certificate{ObjectMeta: metav1.ObjectMeta{Name: "cert-a", Namespace: ns, Labels: map[string]string{"app": "web"}}})
	if len(requests) != 1 {
		t.Fatalf("expected one request, got %d", len(requests))
	}
	if requests[0].Name != "match" || requests[0].Namespace != ns {
		t.Fatalf("unexpected reconcile request: %+v", requests[0])
	}

	if reqs := r.mapCertificateToCertificateJobs(context.Background(), &corev1.Secret{}); reqs != nil {
		t.Fatalf("expected non-certificate object to produce nil requests")
	}
}

func TestMapSecretToCertificateJobsDeduplicatesRequests(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)
	ns := "default"
	secretName := "tls-secret"

	certA := &certmgrv1.Certificate{ObjectMeta: metav1.ObjectMeta{Name: "cert-a", Namespace: ns, Labels: map[string]string{"app": "web"}}, Spec: certmgrv1.CertificateSpec{SecretName: secretName}}
	certB := &certmgrv1.Certificate{ObjectMeta: metav1.ObjectMeta{Name: "cert-b", Namespace: ns, Labels: map[string]string{"app": "web"}}, Spec: certmgrv1.CertificateSpec{SecretName: secretName}}
	cjob := &certificatesv1alpha1.CertificateJob{
		ObjectMeta: metav1.ObjectMeta{Name: "match", Namespace: ns},
		Spec:       certificatesv1alpha1.CertificateJobSpec{CertificateSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
	}

	builder := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(certA, certB, cjob).
		WithIndex(&certmgrv1.Certificate{}, certificateSecretNameField, func(obj client.Object) []string {
			cert, ok := obj.(*certmgrv1.Certificate)
			if !ok || cert.Spec.SecretName == "" {
				return nil
			}
			return []string{cert.Spec.SecretName}
		})

	r := &CertificateJobReconciler{Client: builder.Build(), Scheme: scheme}

	requests := r.mapSecretToCertificateJobs(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns}})
	if len(requests) != 1 {
		t.Fatalf("expected one deduplicated request, got %d", len(requests))
	}
	if requests[0].Name != "match" {
		t.Fatalf("unexpected reconcile request: %+v", requests[0])
	}

	if reqs := r.mapSecretToCertificateJobs(context.Background(), certA); reqs != nil {
		t.Fatalf("expected non-secret object to produce nil requests")
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add batchv1 to scheme: %v", err)
	}
	if err := certmgrv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add cert-manager v1 to scheme: %v", err)
	}
	if err := certificatesv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add certificatejob API to scheme: %v", err)
	}
	return scheme
}
