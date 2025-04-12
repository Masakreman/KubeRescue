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

// desired state of LogRemediation
type LogRemediationSpec struct {
	// Where to collect logs from
	// +kubebuilder:validation:Required
	Sources []LogSource `json:"sources"`

	// elasticsearch connection details
	// +kubebuilder:validation:Required
	ElasticsearchConfig ElasticsearchConfig `json:"elasticsearchConfig"`

	// fluentbit configuration
	// +kubebuilder:validation:Optional
	FluentbitConfig *FluentbitConfig `json:"fluentbitConfig,omitempty"`

	// remediation action
	// +kubebuilder:validation:Optional
	RemediationRules []RemediationRule `json:"remediationRules,omitempty"`
}

// source of logs
type LogSource struct {
	// type of source I.E. Pod, deployment
	// +kubebuilder:validation:Enum=pod;deployment;namespace
	// +kubebuilder:validation:Required
	Type string `json:"type"`

	// match resource
	// +kubebuilder:validation:Required
	Selector map[string]string `json:"selector"`

	//container to get logs. can leave empty to collect from all containers
	// +kubebuilder:validation:Optional
	Container string `json:"container,omitempty"`

	// path to collect logs
	// +kubebuilder:validation:Optional
	Path string `json:"path,omitempty"`
}

// elasticsearch cfg fields
type ElasticsearchConfig struct {
	// host
	// +kubebuilder:validation:Required
	Host string `json:"host"`

	// Port
	// +kubebuilder:default=9200
	// +kubebuilder:validation:Optional
	Port int32 `json:"port,omitempty"`

	// Index to store logs
	// +kubebuilder:validation:Required
	Index string `json:"index"`

	// SecretRef for Auth
	// +kubebuilder:validation:Optional
	SecretRef string `json:"secretRef,omitempty"`
}

// define optional config
type FluentbitConfig struct {
	// BufferSize
	// +kubebuilder:default="5MB"
	// +kubebuilder:validation:Optional
	BufferSize string `json:"bufferSize,omitempty"`

	// FlushInterval
	// +kubebuilder:default=5
	// +kubebuilder:validation:Optional
	FlushInterval int32 `json:"flushInterval,omitempty"`

	// Custom parser config
	// +kubebuilder:validation:Optional
	Parser string `json:"parser,omitempty"`
}

// observed state
type LogRemediationStatus struct {
	// last observation of the state
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// current running pods
	FluentbitPods []string `json:"fluentbitPods,omitempty"`

	//last configuration change
	LastConfigured *metav1.Time `json:"lastConfigured,omitempty"`

	// record actions
	RemediationHistory []RemediationHistoryEntry `json:"remediationHistory,omitempty"`

	// most recent generation observed
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// timestamp of last processed log
	LastProcessedTimestamp *metav1.Time `json:"lastProcessedTimestamp,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"

// schema for logremediations API
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

// rule to remediate issues based on log patterns
type RemediationRule struct {
	// regex pattern to look for
	// +kubebuilder:validation:Required
	ErrorPattern string `json:"errorPattern"`

	// Action to take when pattern is matched e.g restart
	// +kubebuilder:validation:Enum=restart;scale;exec;recovery
	// +kubebuilder:validation:Required
	Action string `json:"action"`

	// cooldown in seconds between remediations
	// +kubebuilder:default=60
	// +kubebuilder:validation:Optional
	CooldownPeriod int32 `json:"cooldownPeriod,omitempty"`
}

// record remediation action
type RemediationHistoryEntry struct {
	// Timestamp of remediation action
	Timestamp metav1.Time `json:"timestamp"`

	// PodName that was remediated
	PodName string `json:"podName"`

	// Pattern that triggered remediation
	Pattern string `json:"pattern"`

	// Action taken
	Action string `json:"action"`
}
