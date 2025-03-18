package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LogRemediation defines a log remediation resource
type LogRemediation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LogRemediationSpec   `json:"spec"`
	Status LogRemediationStatus `json:"status,omitempty"`
}

// LogRemediationSpec defines the desired state of LogRemediation
type LogRemediationSpec struct {
	// Target defines the application to monitor
	Target TargetRef `json:"target"`

	// LogPatterns define the log patterns to match
	LogPatterns []LogPattern `json:"logPatterns"`

	// RemediationActions define actions to take on match
	RemediationActions []RemediationAction `json:"remediationActions"`

	// AlertConfig defines alerting configuration
	AlertConfig *AlertConfig `json:"alertConfig,omitempty"`

	// RunbookRef references a runbook to use for remediation
	RunbookRef *RunbookRef `json:"runbookRef,omitempty"`
}

// TargetRef defines the target for log remediation
type TargetRef struct {
	// Kind of the target resource
	Kind string `json:"kind"`

	// Name of the target resource
	Name string `json:"name"`

	// Container to monitor logs from, if applicable
	Container string `json:"container,omitempty"`

	// LogSourceType specifies the type of log source
	LogSourceType string `json:"logSourceType"`

	// LogPath specifies where logs are located
	// Empty means use standard output
	LogPath string `json:"logPath,omitempty"`
}

// LogPattern defines a pattern to match in logs
type LogPattern struct {
	// Name of the pattern
	Name string `json:"name"`

	// Pattern to match, can be regex
	Pattern string `json:"pattern"`

	// Severity of the issue
	Severity string `json:"severity"`

	// Description of the pattern/issue
	Description string `json:"description,omitempty"`

	// Type of the log (JSON, Plain)
	LogType string `json:"logType"`
}

// RemediationAction defines an action to take
type RemediationAction struct {
	// Type of remediation action
	Type string `json:"type"`

	// Parameters for the action
	Parameters map[string]string `json:"parameters,omitempty"`

	// When to execute this action
	When *RemediationCondition `json:"when,omitempty"`

	// Timeout for the action
	Timeout *metav1.Duration `json:"timeout,omitempty"`
}

// RemediationCondition defines when to execute a remediation
type RemediationCondition struct {
	// PatternMatches is the name of the pattern that must match
	PatternMatches string `json:"patternMatches,omitempty"`

	// Occurrences is the number of times the pattern must match
	Occurrences int `json:"occurrences,omitempty"`

	// TimeWindow is the time window for occurrences
	TimeWindow *metav1.Duration `json:"timeWindow,omitempty"`
}

// AlertConfig defines the alerting configuration
type AlertConfig struct {
	// Provider is the alert provider to use
	Provider string `json:"provider"`

	// Endpoint is the endpoint for the provider
	Endpoint string `json:"endpoint"`

	// Parameters for the alert
	Parameters map[string]string `json:"parameters,omitempty"`
}

// RunbookRef defines a reference to a remediation runbook
type RunbookRef struct {
	// Name of the runbook
	Name string `json:"name"`

	// ConfigMap containing the runbook
	ConfigMap string `json:"configMap"`

	// Key in the ConfigMap
	Key string `json:"key,omitempty"`
}

// LogRemediationStatus defines the observed state of LogRemediation
type LogRemediationStatus struct {
	// Conditions represent the latest available observations of an object's state
	Conditions []LogRemediationCondition `json:"conditions,omitempty"`

	// LastRemediationTime is the last time a remediation was performed
	LastRemediationTime *metav1.Time `json:"lastRemediationTime,omitempty"`

	// MatchedPatterns represents patterns that have been matched
	MatchedPatterns []MatchedPattern `json:"matchedPatterns,omitempty"`

	// RemediationHistory contains a history of remediations
	RemediationHistory []RemediationHistoryEntry `json:"remediationHistory,omitempty"`
}

// LogRemediationCondition represents the condition of the log remediation
type LogRemediationCondition struct {
	// Type of the condition
	Type string `json:"type"`

	// Status of the condition, one of True, False, Unknown
	Status string `json:"status"`

	// Last time we got an update on this condition
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`

	// Reason for the condition's last transition
	Reason string `json:"reason,omitempty"`

	// Message about the condition's last transition
	Message string `json:"message,omitempty"`
}

// MatchedPattern represents a matched log pattern
type MatchedPattern struct {
	// Name of the pattern
	Name string `json:"name"`

	// OccurrenceCount is the number of times this pattern was matched
	OccurrenceCount int `json:"occurrenceCount"`

	// LastOccurrence is when this pattern was last matched
	LastOccurrence metav1.Time `json:"lastOccurrence"`

	// Sample is a sample of the matched log
	Sample string `json:"sample,omitempty"`
}

// RemediationHistoryEntry represents a single remediation action taken
type RemediationHistoryEntry struct {
	// Timestamp when the remediation was performed
	Timestamp metav1.Time `json:"timestamp"`

	// Action that was taken
	Action string `json:"action"`

	// Result of the action
	Result string `json:"result"`

	// PatternName that triggered this remediation
	PatternName string `json:"patternName"`

	// Message provides additional details
	Message string `json:"message,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LogRemediationList contains a list of LogRemediation
type LogRemediationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LogRemediation `json:"items"`
}
