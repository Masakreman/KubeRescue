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
)

func init() {
	// Register metrics with the controller-runtime metrics registry
	metrics.Registry.MustRegister(
		RemediationsTotal,
		RemediationLatency,
		LogProcessingErrors,
		ActiveRemediations,
	)
}
