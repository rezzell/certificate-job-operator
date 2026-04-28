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
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FailurePolicy defines workflow behavior when a node fails.
// +kubebuilder:validation:Enum=StopDownstream;ContinueIndependent;BestEffort
// +kubebuilder:default:=StopDownstream
type FailurePolicy string

const (
	FailurePolicyStopDownstream      FailurePolicy = "StopDownstream"
	FailurePolicyContinueIndependent FailurePolicy = "ContinueIndependent"
	FailurePolicyBestEffort          FailurePolicy = "BestEffort"
)

// ExecutionPhase describes the state of a workflow or node.
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Skipped
type ExecutionPhase string

const (
	ExecutionPhasePending   ExecutionPhase = "Pending"
	ExecutionPhaseRunning   ExecutionPhase = "Running"
	ExecutionPhaseSucceeded ExecutionPhase = "Succeeded"
	ExecutionPhaseFailed    ExecutionPhase = "Failed"
	ExecutionPhaseSkipped   ExecutionPhase = "Skipped"
)

// CertificateJobSpec defines the desired state of CertificateJob.
type CertificateJobSpec struct {
	// CertificateSelector selects the Certificate resources that should trigger this workflow.
	CertificateSelector metav1.LabelSelector `json:"certificateSelector,omitempty"`
	// Jobs defines named Job templates used by the workflow DAG.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	Jobs []CertificateJobTemplate `json:"jobs"`
	// Workflow defines DAG edges between job nodes.
	Workflow CertificateWorkflowSpec `json:"workflow,omitempty"`
	// Parallelism limits how many runnable nodes can be created at once.
	// +kubebuilder:default:=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=20
	Parallelism *int32 `json:"parallelism,omitempty"`
	// JobTTLSecondsAfterFinished sets a default TTL for created Jobs if not specified in the template.
	// +kubebuilder:default:=3600
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=604800
	JobTTLSecondsAfterFinished *int32 `json:"jobTTLSecondsAfterFinished,omitempty"`
	// FailurePolicy controls how the workflow proceeds after failures.
	FailurePolicy FailurePolicy `json:"failurePolicy,omitempty"`
}

// CertificateJobTemplate is a named Kubernetes Job template.
type CertificateJobTemplate struct {
	// Name is the workflow node name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	Name string `json:"name"`
	// Labels are merged into created Job metadata labels.
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations are merged into created Job metadata annotations.
	Annotations map[string]string `json:"annotations,omitempty"`
	// Template is the Kubernetes Job spec to run.
	Template batchv1.JobSpec `json:"template"`
}

// CertificateWorkflowSpec defines DAG connectivity between jobs.
type CertificateWorkflowSpec struct {
	// Edges is a list of directed dependencies (from -> to).
	Edges []CertificateWorkflowEdge `json:"edges,omitempty"`
}

// CertificateWorkflowEdge defines a dependency between two job nodes.
type CertificateWorkflowEdge struct {
	// From is the dependency node.
	// +kubebuilder:validation:MinLength=1
	From string `json:"from"`
	// To is the dependent node.
	// +kubebuilder:validation:MinLength=1
	To string `json:"to"`
}

// CertificateJobStatus defines the observed state of CertificateJob.
type CertificateJobStatus struct {
	// ObservedCertificates tracks workflow state per matching certificate.
	// +listType=map
	// +listMapKey=namespace
	// +listMapKey=name
	ObservedCertificates []CertificateExecutionState `json:"observedCertificates,omitempty"`
	// Conditions represent overall controller state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// CertificateExecutionState tracks workflow execution for one Certificate.
type CertificateExecutionState struct {
	// Namespace is the Certificate namespace.
	Namespace string `json:"namespace"`
	// Name is the Certificate name.
	Name string `json:"name"`
	// SecretName is the referenced TLS secret.
	SecretName string `json:"secretName,omitempty"`
	// InputHash is the hash used for dedup/rerun detection.
	InputHash string `json:"inputHash,omitempty"`
	// RunID is a short identifier for this execution.
	RunID string `json:"runID,omitempty"`
	// Phase is the current workflow phase for this certificate.
	Phase ExecutionPhase `json:"phase,omitempty"`
	// Message contains human-readable details for failures or skips.
	Message string `json:"message,omitempty"`
	// LastTriggeredTime is when the current hash run started.
	LastTriggeredTime *metav1.Time `json:"lastTriggeredTime,omitempty"`
	// LastCompletedTime is when the current hash run completed.
	LastCompletedTime *metav1.Time `json:"lastCompletedTime,omitempty"`
	// Nodes tracks node-level execution.
	// +listType=map
	// +listMapKey=name
	Nodes []WorkflowNodeState `json:"nodes,omitempty"`
}

// WorkflowNodeState tracks one workflow node execution state.
type WorkflowNodeState struct {
	// Name is the node name.
	Name string `json:"name"`
	// JobName is the Kubernetes Job created for this node.
	JobName string `json:"jobName,omitempty"`
	// Phase is the current node phase.
	Phase ExecutionPhase `json:"phase,omitempty"`
	// Message contains failure/skip detail for this node.
	Message string `json:"message,omitempty"`
	// StartedAt is when Job creation started.
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// CompletedAt is when node reached terminal phase.
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=cjob
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Progressing",type="string",JSONPath=".status.conditions[?(@.type=='Progressing')].status"
// +kubebuilder:printcolumn:name="Degraded",type="string",JSONPath=".status.conditions[?(@.type=='Degraded')].status"

// CertificateJob is the Schema for the certificatejobs API.
type CertificateJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CertificateJobSpec   `json:"spec,omitempty"`
	Status CertificateJobStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CertificateJobList contains a list of CertificateJob.
type CertificateJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CertificateJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CertificateJob{}, &CertificateJobList{})
}
