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

	// ElasticsearchConfig defines the Elasticsearch connection details
	// +kubebuilder:validation:Required
	ElasticsearchConfig ElasticsearchConfig `json:"elasticsearchConfig"`

	// FluentbitConfig defines the Fluentbit configuration
	// +kubebuilder:validation:Optional
	FluentbitConfig *FluentbitConfig `json:"fluentbitConfig,omitempty"`
}

// LogSource defines a source to collect logs from
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

// ElasticsearchConfig defines the Elasticsearch connection details
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

// FluentbitConfig defines optional custom Fluentbit configuration
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

// LogRemediationStatus defines the observed state of LogRemediation
type LogRemediationStatus struct {
	// Conditions represent the latest available observations of an object's state
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// FluentbitPods lists the pods running Fluentbit for this remediation
	FluentbitPods []string `json:"fluentbitPods,omitempty"`

	// LastConfigured is the last time the remediation was configured
	LastConfigured *metav1.Time `json:"lastConfigured,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"

// LogRemediation is the Schema for the logremediations API
type LogRemediation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LogRemediationSpec   `json:"spec,omitempty"`
	Status LogRemediationStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// LogRemediationList contains a list of LogRemediation
type LogRemediationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LogRemediation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LogRemediation{}, &LogRemediationList{})
}
