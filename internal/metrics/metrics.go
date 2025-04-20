package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Metrics for the KubeRescue controller
var (
	// RemediationsTotal tracks the total number of remediation actions performed
	RemediationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kuberescue_remediations_total",
			Help: "The total number of remediation actions performed",
		},
		[]string{"action", "error_pattern", "namespace"},
	)

	// RemediationLatency tracks the time taken to perform remediation actions
	RemediationLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kuberescue_remediation_latency_seconds",
			Help:    "Time taken to perform remediation actions",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"action", "namespace"},
	)

	// LogProcessingErrors tracks errors encountered during log processing
	LogProcessingErrors = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "kuberescue_log_processing_errors_total",
			Help: "Total number of errors encountered while processing logs",
		},
	)

	// ActiveRemediations tracks the number of active remediations
	ActiveRemediations = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kuberescue_active_remediations",
			Help: "Number of active LogRemediation resources by namespace",
		},
		[]string{"namespace"},
	)

	// Enhanced metrics for better visualization

	// ErrorPatternOccurrences tracks the frequency of specific error patterns
	ErrorPatternOccurrences = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kuberescue_error_pattern_occurrences_total",
			Help: "Number of times each error pattern was detected in logs",
		},
		[]string{"pattern", "namespace", "application"},
	)

	// ResourceScalingOperations tracks scaling operations performed
	ResourceScalingOperations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kuberescue_scaling_operations_total",
			Help: "Number of scaling operations performed",
		},
		[]string{"resource_type", "resource_name", "namespace", "direction"},
	)

	// ResourceCurrentReplicas tracks current replica counts for resources
	ResourceCurrentReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kuberescue_resource_current_replicas",
			Help: "Current replica count for resources managed by KubeRescue",
		},
		[]string{"resource_type", "resource_name", "namespace"},
	)

	// LogsProcessedTotal tracks the total number of logs processed
	LogsProcessedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "kuberescue_logs_processed_total",
			Help: "Total number of log entries processed",
		},
	)

	// LogProcessingDuration tracks time taken to process log batches
	LogProcessingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kuberescue_log_processing_duration_seconds",
			Help:    "Time taken to process log batches",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 10), // 0.01s to ~10s
		},
		[]string{"namespace"},
	)

	// RemediationSuccessTotal tracks successful remediation actions
	RemediationSuccessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kuberescue_remediation_success_total",
			Help: "Number of successful remediation actions",
		},
		[]string{"action", "namespace"},
	)

	// RemediationFailureTotal tracks failed remediation actions
	RemediationFailureTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kuberescue_remediation_failure_total",
			Help: "Number of failed remediation actions",
		},
		[]string{"action", "namespace", "reason"},
	)

	// RemediationsInCooldown tracks number of remediation actions in cooldown
	RemediationsInCooldown = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kuberescue_remediations_in_cooldown",
			Help: "Number of remediation actions currently in cooldown period",
		},
		[]string{"action", "namespace"},
	)
)

func init() {
	// Register metrics with the controller-runtime metrics registry
	metrics.Registry.MustRegister(
		RemediationsTotal,
		RemediationLatency,
		LogProcessingErrors,
		ActiveRemediations,
		ErrorPatternOccurrences,
		ResourceScalingOperations,
		ResourceCurrentReplicas,
		LogsProcessedTotal,
		LogProcessingDuration,
		RemediationSuccessTotal,
		RemediationFailureTotal,
		RemediationsInCooldown,
	)
}
