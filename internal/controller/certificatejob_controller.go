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

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	certmgrv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"

	certificatesv1alpha1 "github.com/russell/certificate-job-operator/api/v1alpha1"
)

const (
	certificateSecretNameField = "spec.secretName"

	conditionReady       = "Ready"
	conditionProgressing = "Progressing"
	conditionDegraded    = "Degraded"

	secretVolumeName = "certificate-input"
	secretMountPath  = "/var/run/certificate-input"
)

// CertificateJobReconciler reconciles a CertificateJob object.
type CertificateJobReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=certificates.rezzell.com,namespace=system,resources=certificatejobs,verbs=get;list;watch
// +kubebuilder:rbac:groups=certificates.rezzell.com,namespace=system,resources=certificatejobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cert-manager.io,namespace=system,resources=certificates,verbs=get;list;watch
// +kubebuilder:rbac:groups="",namespace=system,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,namespace=system,resources=jobs,verbs=get;list;watch;create

func (r *CertificateJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	certificateJob := &certificatesv1alpha1.CertificateJob{}
	if err := r.Get(ctx, req.NamespacedName, certificateJob); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	baseStatus := certificateJob.Status
	result, reconcileErr := r.reconcileCertificateJob(ctx, certificateJob)

	if !equalCertificateJobStatus(baseStatus, certificateJob.Status) {
		if err := r.Status().Update(ctx, certificateJob); err != nil {
			log.Error(err, "unable to update CertificateJob status")
			return ctrl.Result{}, err
		}
	}

	if reconcileErr != nil {
		log.Error(reconcileErr, "reconcile failed")
		return ctrl.Result{}, reconcileErr
	}

	return result, nil
}

func (r *CertificateJobReconciler) reconcileCertificateJob(ctx context.Context, certificateJob *certificatesv1alpha1.CertificateJob) (ctrl.Result, error) {
	now := metav1.Now()

	deps, reverseDeps, err := buildWorkflowGraph(certificateJob)
	if err != nil {
		setCondition(&certificateJob.Status.Conditions, conditionReady, metav1.ConditionFalse, "InvalidSpec", err.Error(), certificateJob.Generation)
		setCondition(&certificateJob.Status.Conditions, conditionProgressing, metav1.ConditionFalse, "InvalidSpec", "reconciliation blocked by invalid spec", certificateJob.Generation)
		setCondition(&certificateJob.Status.Conditions, conditionDegraded, metav1.ConditionTrue, "InvalidSpec", err.Error(), certificateJob.Generation)
		return ctrl.Result{}, nil
	}

	certificates, err := r.listMatchingCertificates(ctx, certificateJob)
	if err != nil {
		setCondition(&certificateJob.Status.Conditions, conditionReady, metav1.ConditionFalse, "ListFailed", err.Error(), certificateJob.Generation)
		setCondition(&certificateJob.Status.Conditions, conditionProgressing, metav1.ConditionFalse, "ListFailed", "unable to list matching certificates", certificateJob.Generation)
		setCondition(&certificateJob.Status.Conditions, conditionDegraded, metav1.ConditionTrue, "ListFailed", err.Error(), certificateJob.Generation)
		return ctrl.Result{}, err
	}

	stateIndex := make(map[string]int, len(certificateJob.Status.ObservedCertificates))
	for i := range certificateJob.Status.ObservedCertificates {
		entry := certificateJob.Status.ObservedCertificates[i]
		stateIndex[certificateKey(entry.Namespace, entry.Name)] = i
	}

	matchedKeys := sets.New[string]()
	needsRequeue := false
	anyFailed := false
	anyInProgress := false

	for i := range certificates {
		cert := certificates[i]
		certKey := certificateKey(cert.Namespace, cert.Name)
		matchedKeys.Insert(certKey)

		idx, ok := stateIndex[certKey]
		if !ok {
			certificateJob.Status.ObservedCertificates = append(certificateJob.Status.ObservedCertificates, certificatesv1alpha1.CertificateExecutionState{
				Namespace: cert.Namespace,
				Name:      cert.Name,
				Phase:     certificatesv1alpha1.ExecutionPhasePending,
			})
			idx = len(certificateJob.Status.ObservedCertificates) - 1
			stateIndex[certKey] = idx
		}
		state := &certificateJob.Status.ObservedCertificates[idx]

		if cert.Spec.SecretName == "" {
			state.SecretName = ""
			state.Phase = certificatesv1alpha1.ExecutionPhaseFailed
			state.Message = "certificate.spec.secretName is empty"
			state.LastCompletedTime = &now
			anyFailed = true
			continue
		}

		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: cert.Namespace, Name: cert.Spec.SecretName}, secret); err != nil {
			state.SecretName = cert.Spec.SecretName
			state.Phase = certificatesv1alpha1.ExecutionPhaseFailed
			state.Message = fmt.Sprintf("unable to read secret %s/%s: %v", cert.Namespace, cert.Spec.SecretName, err)
			state.LastCompletedTime = &now
			anyFailed = true
			continue
		}

		inputHash, err := buildInputHash(&cert, secret)
		if err != nil {
			state.SecretName = cert.Spec.SecretName
			state.Phase = certificatesv1alpha1.ExecutionPhaseFailed
			state.Message = fmt.Sprintf("unable to calculate input hash: %v", err)
			state.LastCompletedTime = &now
			anyFailed = true
			continue
		}

		if state.InputHash != inputHash {
			state.SecretName = cert.Spec.SecretName
			state.InputHash = inputHash
			state.RunID = shortHash(inputHash, 12)
			state.Phase = certificatesv1alpha1.ExecutionPhasePending
			state.Message = ""
			state.LastTriggeredTime = &now
			state.LastCompletedTime = nil
			state.Nodes = initializeNodeStates(certificateJob)
		}

		if isTerminalPhase(state.Phase) {
			if state.Phase == certificatesv1alpha1.ExecutionPhaseFailed {
				anyFailed = true
			}
			continue
		}

		running, failed, err := r.reconcileCertificateRun(ctx, certificateJob, &cert, state, deps, reverseDeps)
		if err != nil {
			state.Phase = certificatesv1alpha1.ExecutionPhaseFailed
			state.Message = err.Error()
			state.LastCompletedTime = &now
			anyFailed = true
			continue
		}
		if running {
			anyInProgress = true
			needsRequeue = true
		}
		if failed || state.Phase == certificatesv1alpha1.ExecutionPhaseFailed {
			anyFailed = true
		}
	}

	filtered := make([]certificatesv1alpha1.CertificateExecutionState, 0, len(certificateJob.Status.ObservedCertificates))
	for i := range certificateJob.Status.ObservedCertificates {
		entry := certificateJob.Status.ObservedCertificates[i]
		if matchedKeys.Has(certificateKey(entry.Namespace, entry.Name)) {
			filtered = append(filtered, entry)
		}
	}
	certificateJob.Status.ObservedCertificates = filtered

	reason := "Reconciled"
	msg := "certificate-job workflow is ready"
	if len(certificates) == 0 {
		reason = "NoMatchingCertificates"
		msg = "no matching certificates"
	}

	if anyFailed {
		setCondition(&certificateJob.Status.Conditions, conditionDegraded, metav1.ConditionTrue, "WorkflowFailed", "one or more certificate workflows failed", certificateJob.Generation)
		setCondition(&certificateJob.Status.Conditions, conditionReady, metav1.ConditionFalse, "WorkflowFailed", "one or more certificate workflows failed", certificateJob.Generation)
	} else {
		setCondition(&certificateJob.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "AsExpected", "no failed workflows", certificateJob.Generation)
		setCondition(&certificateJob.Status.Conditions, conditionReady, metav1.ConditionTrue, reason, msg, certificateJob.Generation)
	}

	if anyInProgress {
		setCondition(&certificateJob.Status.Conditions, conditionProgressing, metav1.ConditionTrue, "WorkflowRunning", "one or more workflows are running", certificateJob.Generation)
	} else {
		setCondition(&certificateJob.Status.Conditions, conditionProgressing, metav1.ConditionFalse, "AsExpected", "no workflows are running", certificateJob.Generation)
	}

	if needsRequeue {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

func (r *CertificateJobReconciler) listMatchingCertificates(ctx context.Context, cjob *certificatesv1alpha1.CertificateJob) ([]certmgrv1.Certificate, error) {
	certSelector, err := metav1.LabelSelectorAsSelector(&cjob.Spec.CertificateSelector)
	if err != nil {
		return nil, fmt.Errorf("invalid certificateSelector: %w", err)
	}

	list := &certmgrv1.CertificateList{}
	opts := []client.ListOption{client.InNamespace(cjob.Namespace)}
	if !isSelectorEmpty(certSelector) {
		opts = append(opts, client.MatchingLabelsSelector{Selector: certSelector})
	}
	if err := r.List(ctx, list, opts...); err != nil {
		return nil, err
	}
	all := append([]certmgrv1.Certificate{}, list.Items...)

	sort.Slice(all, func(i, j int) bool {
		if all[i].Namespace == all[j].Namespace {
			return all[i].Name < all[j].Name
		}
		return all[i].Namespace < all[j].Namespace
	})

	return all, nil
}

func (r *CertificateJobReconciler) reconcileCertificateRun(
	ctx context.Context,
	cjob *certificatesv1alpha1.CertificateJob,
	certificate *certmgrv1.Certificate,
	state *certificatesv1alpha1.CertificateExecutionState,
	deps map[string][]string,
	reverseDeps map[string][]string,
) (bool, bool, error) {
	nodes := nodeStateMap(state)
	for _, tmpl := range cjob.Spec.Jobs {
		if _, ok := nodes[tmpl.Name]; !ok {
			state.Nodes = append(state.Nodes, certificatesv1alpha1.WorkflowNodeState{Name: tmpl.Name, Phase: certificatesv1alpha1.ExecutionPhasePending})
		}
	}
	nodes = nodeStateMap(state)

	now := metav1.Now()
	activeCount := 0
	failedNodes := sets.New[string]()
	defaultTTL := int32(3600)
	if cjob.Spec.JobTTLSecondsAfterFinished != nil {
		defaultTTL = *cjob.Spec.JobTTLSecondsAfterFinished
	}

	for i := range state.Nodes {
		node := &state.Nodes[i]
		if node.JobName == "" {
			continue
		}

		job := &batchv1.Job{}
		err := r.Get(ctx, types.NamespacedName{Namespace: certificate.Namespace, Name: node.JobName}, job)
		if err != nil {
			if apierrors.IsNotFound(err) {
				node.Phase = certificatesv1alpha1.ExecutionPhaseFailed
				node.Message = "job disappeared"
				node.CompletedAt = &now
				failedNodes.Insert(node.Name)
				continue
			}
			return false, true, err
		}

		if isJobComplete(job) {
			node.Phase = certificatesv1alpha1.ExecutionPhaseSucceeded
			node.Message = ""
			node.CompletedAt = &now
			continue
		}
		if isJobFailed(job) {
			node.Phase = certificatesv1alpha1.ExecutionPhaseFailed
			node.Message = "job failed"
			node.CompletedAt = &now
			failedNodes.Insert(node.Name)
			continue
		}

		if job.Status.Active > 0 {
			node.Phase = certificatesv1alpha1.ExecutionPhaseRunning
			activeCount++
			continue
		}

		if node.Phase == certificatesv1alpha1.ExecutionPhasePending {
			node.Phase = certificatesv1alpha1.ExecutionPhaseRunning
			activeCount++
		}
	}

	if failedNodes.Len() > 0 {
		applyFailurePolicy(cjob.Spec.FailurePolicy, state, deps, reverseDeps, failedNodes.UnsortedList())
	}

	parallelism := int32(1)
	if cjob.Spec.Parallelism != nil {
		parallelism = *cjob.Spec.Parallelism
	}
	if parallelism < 1 {
		parallelism = 1
	}

	availableSlots := int(parallelism) - activeCount
	if availableSlots > 0 {
		jobTemplates := make(map[string]certificatesv1alpha1.CertificateJobTemplate, len(cjob.Spec.Jobs))
		for _, j := range cjob.Spec.Jobs {
			jobTemplates[j.Name] = j
		}

		runnable := runnableNodes(cjob.Spec.FailurePolicy, state, deps)
		sort.Strings(runnable)

		for _, nodeName := range runnable {
			if availableSlots == 0 {
				break
			}
			node := nodes[nodeName]
			template := jobTemplates[nodeName]
			jobName := buildJobName(cjob.Name, certificate.Name, nodeName, state.RunID)

			existing := &batchv1.Job{}
			err := r.Get(ctx, types.NamespacedName{Namespace: certificate.Namespace, Name: jobName}, existing)
			if err != nil && !apierrors.IsNotFound(err) {
				return true, false, err
			}

			if apierrors.IsNotFound(err) {
				baseLabels := map[string]string{
					"app.kubernetes.io/managed-by":                   "certificate-job-operator",
					"certificates.rezzell.com/certificatejob":        cjob.Name,
					"certificates.rezzell.com/certificate":           certificate.Name,
					"certificates.rezzell.com/certificate-namespace": certificate.Namespace,
					"certificates.rezzell.com/workflow-node":         nodeName,
					"certificates.rezzell.com/run-id":                state.RunID,
				}

				job := &batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:        jobName,
						Namespace:   certificate.Namespace,
						Labels:      mergeMaps(baseLabels, template.Labels),
						Annotations: mergeMaps(nil, template.Annotations),
					},
					Spec: *template.Template.DeepCopy(),
				}
				job.Spec.Template.Labels = mergeMaps(baseLabels, job.Spec.Template.Labels)

				hardenJobTemplate(&job.Spec, defaultTTL)
				injectCertificateSecret(&job.Spec, certificate.Spec.SecretName)
				if err := controllerutil.SetControllerReference(cjob, job, r.Scheme); err != nil {
					return true, false, err
				}
				if err := r.Create(ctx, job); err != nil {
					return true, false, err
				}
				r.Recorder.Eventf(cjob, corev1.EventTypeNormal, "JobCreated", "Created job %s for certificate %s/%s", job.Name, certificate.Namespace, certificate.Name)
			}

			node.JobName = jobName
			node.Phase = certificatesv1alpha1.ExecutionPhaseRunning
			node.Message = ""
			node.StartedAt = &now
			availableSlots--
			activeCount++
		}
	}

	state.Phase = deriveWorkflowPhase(state)
	if isTerminalPhase(state.Phase) {
		state.LastCompletedTime = &now
	}

	return state.Phase == certificatesv1alpha1.ExecutionPhaseRunning || state.Phase == certificatesv1alpha1.ExecutionPhasePending,
		state.Phase == certificatesv1alpha1.ExecutionPhaseFailed,
		nil
}

func buildWorkflowGraph(cjob *certificatesv1alpha1.CertificateJob) (map[string][]string, map[string][]string, error) {
	if len(cjob.Spec.Jobs) == 0 {
		return nil, nil, fmt.Errorf("spec.jobs must contain at least one template")
	}

	deps := make(map[string][]string, len(cjob.Spec.Jobs))
	reverse := make(map[string][]string, len(cjob.Spec.Jobs))
	jobNames := sets.New[string]()

	for _, job := range cjob.Spec.Jobs {
		if job.Name == "" {
			return nil, nil, fmt.Errorf("job template name cannot be empty")
		}
		if jobNames.Has(job.Name) {
			return nil, nil, fmt.Errorf("duplicate job template name %q", job.Name)
		}
		if err := validateJobTemplateSecurity(&job.Template); err != nil {
			return nil, nil, fmt.Errorf("job %q violates security policy: %w", job.Name, err)
		}
		jobNames.Insert(job.Name)
		deps[job.Name] = []string{}
		reverse[job.Name] = []string{}
	}

	for _, edge := range cjob.Spec.Workflow.Edges {
		if !jobNames.Has(edge.From) || !jobNames.Has(edge.To) {
			return nil, nil, fmt.Errorf("invalid edge %q->%q: both nodes must exist in spec.jobs", edge.From, edge.To)
		}
		if edge.From == edge.To {
			return nil, nil, fmt.Errorf("invalid edge %q->%q: self dependency is not allowed", edge.From, edge.To)
		}
		deps[edge.To] = append(deps[edge.To], edge.From)
		reverse[edge.From] = append(reverse[edge.From], edge.To)
	}

	if err := validateAcyclic(deps); err != nil {
		return nil, nil, err
	}

	return deps, reverse, nil
}

func validateAcyclic(deps map[string][]string) error {
	indegree := make(map[string]int, len(deps))
	outbound := make(map[string][]string, len(deps))
	for node := range deps {
		indegree[node] = 0
	}

	for node, dependencies := range deps {
		for _, dep := range dependencies {
			indegree[node]++
			outbound[dep] = append(outbound[dep], node)
		}
	}

	queue := make([]string, 0, len(deps))
	for node, degree := range indegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}

	visited := 0
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		visited++

		for _, next := range outbound[node] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if visited != len(deps) {
		return fmt.Errorf("workflow edges contain a cycle")
	}
	return nil
}

func deriveWorkflowPhase(state *certificatesv1alpha1.CertificateExecutionState) certificatesv1alpha1.ExecutionPhase {
	hasRunning := false
	hasPending := false
	hasFailed := false
	allTerminal := true

	for _, node := range state.Nodes {
		switch node.Phase {
		case certificatesv1alpha1.ExecutionPhaseFailed:
			hasFailed = true
		case certificatesv1alpha1.ExecutionPhaseRunning:
			hasRunning = true
			allTerminal = false
		case certificatesv1alpha1.ExecutionPhasePending:
			hasPending = true
			allTerminal = false
		case certificatesv1alpha1.ExecutionPhaseSucceeded, certificatesv1alpha1.ExecutionPhaseSkipped:
			// terminal states
		default:
			allTerminal = false
		}
	}

	if hasFailed {
		return certificatesv1alpha1.ExecutionPhaseFailed
	}
	if hasRunning {
		return certificatesv1alpha1.ExecutionPhaseRunning
	}
	if hasPending {
		return certificatesv1alpha1.ExecutionPhasePending
	}
	if allTerminal {
		return certificatesv1alpha1.ExecutionPhaseSucceeded
	}
	return certificatesv1alpha1.ExecutionPhasePending
}

func initializeNodeStates(cjob *certificatesv1alpha1.CertificateJob) []certificatesv1alpha1.WorkflowNodeState {
	nodes := make([]certificatesv1alpha1.WorkflowNodeState, 0, len(cjob.Spec.Jobs))
	for _, job := range cjob.Spec.Jobs {
		nodes = append(nodes, certificatesv1alpha1.WorkflowNodeState{
			Name:  job.Name,
			Phase: certificatesv1alpha1.ExecutionPhasePending,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes
}

func runnableNodes(
	failurePolicy certificatesv1alpha1.FailurePolicy,
	state *certificatesv1alpha1.CertificateExecutionState,
	deps map[string][]string,
) []string {
	nodes := nodeStateMap(state)
	runnable := make([]string, 0)

	for name, node := range nodes {
		if node.Phase != certificatesv1alpha1.ExecutionPhasePending {
			continue
		}

		dependencies := deps[name]
		canRun := true
		for _, dep := range dependencies {
			depPhase := nodes[dep].Phase
			if depPhase == certificatesv1alpha1.ExecutionPhaseSucceeded || depPhase == certificatesv1alpha1.ExecutionPhaseSkipped {
				continue
			}
			if depPhase == certificatesv1alpha1.ExecutionPhaseFailed {
				if failurePolicy == certificatesv1alpha1.FailurePolicyBestEffort {
					continue
				}
				canRun = false
				break
			}
			canRun = false
			break
		}

		if canRun {
			runnable = append(runnable, name)
		}
	}

	return runnable
}

func applyFailurePolicy(
	policy certificatesv1alpha1.FailurePolicy,
	state *certificatesv1alpha1.CertificateExecutionState,
	deps map[string][]string,
	reverse map[string][]string,
	failed []string,
) {
	nodes := nodeStateMap(state)
	if len(failed) == 0 {
		return
	}

	switch policy {
	case certificatesv1alpha1.FailurePolicyBestEffort:
		return
	case certificatesv1alpha1.FailurePolicyContinueIndependent:
		toSkip := descendantNodes(reverse, failed)
		for _, name := range toSkip {
			node := nodes[name]
			if node.Phase == certificatesv1alpha1.ExecutionPhasePending {
				node.Phase = certificatesv1alpha1.ExecutionPhaseSkipped
				node.Message = "skipped due to failed dependency"
				t := metav1.Now()
				node.CompletedAt = &t
			}
		}
	default:
		for _, node := range state.Nodes {
			if node.Phase == certificatesv1alpha1.ExecutionPhasePending {
				n := nodes[node.Name]
				n.Phase = certificatesv1alpha1.ExecutionPhaseSkipped
				n.Message = "skipped because workflow stopped after a failure"
				t := metav1.Now()
				n.CompletedAt = &t
			}
		}
	}

	_ = deps
}

func descendantNodes(reverse map[string][]string, roots []string) []string {
	seen := sets.New[string]()
	queue := append([]string{}, roots...)

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, child := range reverse[node] {
			if seen.Has(child) {
				continue
			}
			seen.Insert(child)
			queue = append(queue, child)
		}
	}

	out := seen.UnsortedList()
	sort.Strings(out)
	return out
}

func nodeStateMap(state *certificatesv1alpha1.CertificateExecutionState) map[string]*certificatesv1alpha1.WorkflowNodeState {
	m := make(map[string]*certificatesv1alpha1.WorkflowNodeState, len(state.Nodes))
	for i := range state.Nodes {
		node := &state.Nodes[i]
		m[node.Name] = node
	}
	return m
}

func buildInputHash(cert *certmgrv1.Certificate, secret *corev1.Secret) (string, error) {
	payload := struct {
		CertificateSpec   certmgrv1.CertificateSpec   `json:"certificateSpec"`
		CertificateStatus certmgrv1.CertificateStatus `json:"certificateStatus"`
		CertificateGen    int64                       `json:"certificateGeneration"`
		SecretType        corev1.SecretType           `json:"secretType"`
		SecretData        map[string][]byte           `json:"secretData"`
	}{
		CertificateSpec:   cert.Spec,
		CertificateStatus: cert.Status,
		CertificateGen:    cert.Generation,
		SecretType:        secret.Type,
		SecretData:        secret.Data,
	}

	serialized, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(serialized)
	return hex.EncodeToString(hash[:]), nil
}

func buildJobName(cjobName, certName, nodeName, runID string) string {
	raw := sanitizeDNS1123(fmt.Sprintf("%s-%s-%s-%s", cjobName, certName, nodeName, runID))
	if len(raw) <= 63 {
		return raw
	}
	return strings.Trim(raw[:63], "-")
}

func sanitizeDNS1123(in string) string {
	lower := strings.ToLower(in)
	b := strings.Builder{}
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "job"
	}
	return out
}

func shortHash(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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

func hardenJobTemplate(spec *batchv1.JobSpec, defaultTTL int32) {
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

func injectCertificateSecret(spec *batchv1.JobSpec, secretName string) {
	if secretName == "" {
		return
	}

	volumeExists := false
	for i := range spec.Template.Spec.Volumes {
		if spec.Template.Spec.Volumes[i].Name == secretVolumeName {
			spec.Template.Spec.Volumes[i].Secret = &corev1.SecretVolumeSource{SecretName: secretName}
			volumeExists = true
			break
		}
	}
	if !volumeExists {
		spec.Template.Spec.Volumes = append(spec.Template.Spec.Volumes, corev1.Volume{
			Name: secretVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: secretName},
			},
		})
	}

	for i := range spec.Template.Spec.InitContainers {
		ensureVolumeMount(&spec.Template.Spec.InitContainers[i], secretVolumeName, secretMountPath)
	}
	for i := range spec.Template.Spec.Containers {
		ensureVolumeMount(&spec.Template.Spec.Containers[i], secretVolumeName, secretMountPath)
	}
}

func ensureVolumeMount(container *corev1.Container, volumeName, path string) {
	for i := range container.VolumeMounts {
		if container.VolumeMounts[i].Name == volumeName {
			container.VolumeMounts[i].MountPath = path
			container.VolumeMounts[i].ReadOnly = true
			return
		}
	}
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      volumeName,
		MountPath: path,
		ReadOnly:  true,
	})
}

func mergeMaps(base map[string]string, extras map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extras {
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isJobComplete(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isJobFailed(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func setCondition(conditions *[]metav1.Condition, conditionType string, status metav1.ConditionStatus, reason, message string, generation int64) {
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
}

func equalCertificateJobStatus(a, b certificatesv1alpha1.CertificateJobStatus) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func certificateKey(namespace, name string) string {
	return namespace + "/" + name
}

func isTerminalPhase(phase certificatesv1alpha1.ExecutionPhase) bool {
	return phase == certificatesv1alpha1.ExecutionPhaseSucceeded || phase == certificatesv1alpha1.ExecutionPhaseFailed || phase == certificatesv1alpha1.ExecutionPhaseSkipped
}

func isSelectorEmpty(selector labels.Selector) bool {
	if selector == nil {
		return true
	}
	return selector.Empty()
}

func (r *CertificateJobReconciler) mapCertificateToCertificateJobs(ctx context.Context, obj client.Object) []reconcile.Request {
	cert, ok := obj.(*certmgrv1.Certificate)
	if !ok {
		return nil
	}

	cjobList := &certificatesv1alpha1.CertificateJobList{}
	if err := r.List(ctx, cjobList, client.InNamespace(cert.Namespace)); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0)
	for i := range cjobList.Items {
		cjob := cjobList.Items[i]

		certSelector, err := metav1.LabelSelectorAsSelector(&cjob.Spec.CertificateSelector)
		if err != nil {
			continue
		}
		if !isSelectorEmpty(certSelector) && !certSelector.Matches(labels.Set(cert.Labels)) {
			continue
		}

		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: cjob.Name, Namespace: cjob.Namespace}})
	}

	return requests
}

func (r *CertificateJobReconciler) mapSecretToCertificateJobs(ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	certList := &certmgrv1.CertificateList{}
	if err := r.List(ctx, certList, client.InNamespace(secret.Namespace), client.MatchingFields{certificateSecretNameField: secret.Name}); err != nil {
		return nil
	}

	requestSet := make(map[string]reconcile.Request)
	for i := range certList.Items {
		reqs := r.mapCertificateToCertificateJobs(ctx, &certList.Items[i])
		for _, req := range reqs {
			requestSet[req.Namespace+"/"+req.Name] = req
		}
	}

	requests := make([]reconcile.Request, 0, len(requestSet))
	for _, req := range requestSet {
		requests = append(requests, req)
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *CertificateJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &certmgrv1.Certificate{}, certificateSecretNameField, func(raw client.Object) []string {
		cert, ok := raw.(*certmgrv1.Certificate)
		if !ok || cert.Spec.SecretName == "" {
			return nil
		}
		return []string{cert.Spec.SecretName}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&certificatesv1alpha1.CertificateJob{}).
		Owns(&batchv1.Job{}).
		Watches(&certmgrv1.Certificate{}, handler.EnqueueRequestsFromMapFunc(r.mapCertificateToCertificateJobs)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapSecretToCertificateJobs)).
		Named("certificatejob").
		Complete(r)
}
