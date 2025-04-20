/*
Copyright 2025.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LogRemediationSpec defines the desired state of LogRemediation
type LogRemediationSpec struct {
	// Sources defines the sources to collect logs from
	// +kubebuilder:validation:Required
	Sources []LogSource `json:"sources"`

	// defines the Elasticsearch connection details
	// +kubebuilder:validation:Required
	ElasticsearchConfig ElasticsearchConfig `json:"elasticsearchConfig"`

	// defines the Fluentbit configuration
	// +kubebuilder:validation:Optional
	FluentbitConfig *FluentbitConfig `json:"fluentbitConfig,omitempty"`

	// defines the remediation actions
	// +kubebuilder:validation:Optional
	RemediationRules []RemediationRule `json:"remediationRules,omitempty"`
}

// defines a source to collect logs from
type LogSource struct {
	// Type of log source (pod, deployment, namespace)
	// +kubebuilder:validation:Enum=pod;deployment;namespace
	// +kubebuilder:validation:Required
	Type string `json:"type"`

	// Selector to match resources
	// +kubebuilder:validation:Required
	Selector map[string]string `json:"selector"`

	// Container to collect logs from (optional, if not specified collect from all containers)
	// +kubebuilder:validation:Optional
	Container string `json:"container,omitempty"`

	// Path to logfiles if using a custom log path
	// +kubebuilder:validation:Optional
	Path string `json:"path,omitempty"`
}

// defines the Elasticsearch connection details
type ElasticsearchConfig struct {
	// Host of the Elasticsearch cluster
	// +kubebuilder:validation:Required
	Host string `json:"host"`

	// Port of the Elasticsearch cluster
	// +kubebuilder:default=9200
	// +kubebuilder:validation:Optional
	Port int32 `json:"port,omitempty"`

	// Index to store logs in
	// +kubebuilder:validation:Required
	Index string `json:"index"`

	// SecretRef for authentication (optional)
	// +kubebuilder:validation:Optional
	SecretRef string `json:"secretRef,omitempty"`
}

// defines optional custom Fluentbit configuration
type FluentbitConfig struct {
	// BufferSize for Fluentbit
	// +kubebuilder:default="5MB"
	// +kubebuilder:validation:Optional
	BufferSize string `json:"bufferSize,omitempty"`

	// FlushInterval for Fluentbit
	// +kubebuilder:default=5
	// +kubebuilder:validation:Optional
	FlushInterval int32 `json:"flushInterval,omitempty"`

	// Custom parser configuration
	// +kubebuilder:validation:Optional
	Parser string `json:"parser,omitempty"`
}

// defines the observed state of LogRemediation
type LogRemediationStatus struct {
	// Conditions represent the latest available observations of an object's state
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// pods running Fluentbit for this remediation
	FluentbitPods []string `json:"fluentbitPods,omitempty"`

	// last time the remediation was configured
	LastConfigured *metav1.Time `json:"lastConfigured,omitempty"`

	// RemediationHistory records all remediation actions
	RemediationHistory []RemediationHistoryEntry `json:"remediationHistory,omitempty"`

	// most recent generation observed for this LogRemediation
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Store the timestamp of the most recent log processed in the CR's status
	LastProcessedTimestamp *metav1.Time `json:"lastProcessedTimestamp,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"

// Schema for the logremediations API
type LogRemediation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LogRemediationSpec   `json:"spec,omitempty"`
	Status LogRemediationStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// list of LogRemediation
type LogRemediationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LogRemediation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LogRemediation{}, &LogRemediationList{})
}

// defines a rule to remediate issues based on log patterns
type RemediationRule struct {
	//regex pattern to match in logs
	// +kubebuilder:validation:Required
	ErrorPattern string `json:"errorPattern"`

	// Action to take when pattern is matched (restart, scale, exec, recovery)
	// +kubebuilder:validation:Enum=restart;scale;exec;recovery
	// +kubebuilder:validation:Required
	Action string `json:"action"`

	// seconds between remediation actions
	// +kubebuilder:default=60
	// +kubebuilder:validation:Optional
	CooldownPeriod int32 `json:"cooldownPeriod,omitempty"`
}

// records a remediation action
type RemediationHistoryEntry struct {
	// Timestamp of the remediation action
	Timestamp metav1.Time `json:"timestamp"`

	// PodName that was remediated
	PodName string `json:"podName"`

	// Pattern that triggered the remediation
	Pattern string `json:"pattern"`

	// Action that was taken
	Action string `json:"action"`
}
