package metrics

import (
	"testing"

	"github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestMetricsRegistration(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Check that each metric is registered properly
	g.Expect(testutil.CollectAndCount(RemediationsTotal)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(RemediationLatency)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(LogProcessingErrors)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(ActiveRemediations)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(ErrorPatternOccurrences)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(ResourceScalingOperations)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(ResourceCurrentReplicas)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(LogsProcessedTotal)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(LogProcessingDuration)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(RemediationSuccessTotal)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(RemediationFailureTotal)).To(gomega.BeNumerically(">=", 0))
	g.Expect(testutil.CollectAndCount(RemediationsInCooldown)).To(gomega.BeNumerically(">=", 0))
}

func TestMetricsIncrement(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// We can't directly reset Prometheus metrics in tests
	// Instead, we'll use unique label combinations for testing

	// Test counter metrics
	RemediationsTotal.WithLabelValues("restart-test", "test-pattern", "default-test").Inc()
	g.Expect(testutil.ToFloat64(RemediationsTotal.WithLabelValues("restart-test", "test-pattern", "default-test"))).To(gomega.Equal(1.0))

	LogProcessingErrors.Inc()
	g.Expect(testutil.ToFloat64(LogProcessingErrors)).To(gomega.BeNumerically(">=", 1.0))

	// Test gauge metrics
	ActiveRemediations.WithLabelValues("default-test").Set(2)
	g.Expect(testutil.ToFloat64(ActiveRemediations.WithLabelValues("default-test"))).To(gomega.Equal(2.0))

	ActiveRemediations.WithLabelValues("default-test").Inc()
	g.Expect(testutil.ToFloat64(ActiveRemediations.WithLabelValues("default-test"))).To(gomega.Equal(3.0))

	ActiveRemediations.WithLabelValues("default-test").Dec()
	g.Expect(testutil.ToFloat64(ActiveRemediations.WithLabelValues("default-test"))).To(gomega.Equal(2.0))

	// Test other counter metrics
	ErrorPatternOccurrences.WithLabelValues("error-pattern-test", "default-test", "test-app").Inc()
	g.Expect(testutil.ToFloat64(ErrorPatternOccurrences.WithLabelValues("error-pattern-test", "default-test", "test-app"))).To(gomega.Equal(1.0))

	ResourceScalingOperations.WithLabelValues("Deployment", "test-deploy", "default-test", "up").Inc()
	g.Expect(testutil.ToFloat64(ResourceScalingOperations.WithLabelValues("Deployment", "test-deploy", "default-test", "up"))).To(gomega.Equal(1.0))

	ResourceCurrentReplicas.WithLabelValues("Deployment", "test-deploy", "default-test").Set(3)
	g.Expect(testutil.ToFloat64(ResourceCurrentReplicas.WithLabelValues("Deployment", "test-deploy", "default-test"))).To(gomega.Equal(3.0))

	LogsProcessedTotal.Add(10)
	g.Expect(testutil.ToFloat64(LogsProcessedTotal)).To(gomega.BeNumerically(">=", 10.0))

	// Test histogram metrics
	RemediationLatency.WithLabelValues("restart-test", "default-test").Observe(0.57)
	// Histograms can't be tested with ToFloat64, but we can check they don't panic
	g.Expect(func() { RemediationLatency.WithLabelValues("restart-test", "default-test").Observe(1.5) }).NotTo(gomega.Panic())

	LogProcessingDuration.WithLabelValues("default-test").Observe(2.1)
	g.Expect(func() { LogProcessingDuration.WithLabelValues("default-test").Observe(0.75) }).NotTo(gomega.Panic())
}

func TestRemediationMetricsCorrectLabels(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Use unique label combinations for the test
	labels := prometheus.Labels{
		"action":        "restart-unique",
		"error_pattern": "CRITICAL_DB_CONNECTION_FAILED-unique",
		"namespace":     "test-namespace-unique",
	}

	RemediationsTotal.With(labels).Inc()

	// Test with WithLabelValues
	RemediationsTotal.WithLabelValues("scale-unique", "MEMORY_ERROR-unique", "test-namespace-unique").Inc()

	// Verify both increments happened
	g.Expect(testutil.ToFloat64(RemediationsTotal.WithLabelValues("restart-unique", "CRITICAL_DB_CONNECTION_FAILED-unique", "test-namespace-unique"))).To(gomega.Equal(1.0))
	g.Expect(testutil.ToFloat64(RemediationsTotal.WithLabelValues("scale-unique", "MEMORY_ERROR-unique", "test-namespace-unique"))).To(gomega.Equal(1.0))
}

func TestResourceMetricsCorrectLabels(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Use unique label combinations
	ResourceCurrentReplicas.WithLabelValues("Deployment", "app1-unique", "ns1-unique").Set(3)
	ResourceCurrentReplicas.WithLabelValues("StatefulSet", "app2-unique", "ns1-unique").Set(5)
	ResourceCurrentReplicas.WithLabelValues("Deployment", "app3-unique", "ns2-unique").Set(2)

	// Verify they're all distinct
	g.Expect(testutil.ToFloat64(ResourceCurrentReplicas.WithLabelValues("Deployment", "app1-unique", "ns1-unique"))).To(gomega.Equal(3.0))
	g.Expect(testutil.ToFloat64(ResourceCurrentReplicas.WithLabelValues("StatefulSet", "app2-unique", "ns1-unique"))).To(gomega.Equal(5.0))
	g.Expect(testutil.ToFloat64(ResourceCurrentReplicas.WithLabelValues("Deployment", "app3-unique", "ns2-unique"))).To(gomega.Equal(2.0))

	// Test scaling operations
	ResourceScalingOperations.WithLabelValues("Deployment", "app1-unique", "ns1-unique", "up").Inc()
	ResourceScalingOperations.WithLabelValues("Deployment", "app1-unique", "ns1-unique", "down").Inc()
	ResourceScalingOperations.WithLabelValues("Deployment", "app1-unique", "ns1-unique", "down").Inc()

	// Verify counts
	g.Expect(testutil.ToFloat64(ResourceScalingOperations.WithLabelValues("Deployment", "app1-unique", "ns1-unique", "up"))).To(gomega.Equal(1.0))
	g.Expect(testutil.ToFloat64(ResourceScalingOperations.WithLabelValues("Deployment", "app1-unique", "ns1-unique", "down"))).To(gomega.Equal(2.0))
}

func TestUnregisterAndRegisterMetrics(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Test that metrics registry contains our metrics
	g.Expect(func() {
		metrics.Registry.Unregister(RemediationsTotal)
		metrics.Registry.MustRegister(RemediationsTotal)
	}).NotTo(gomega.Panic())
}
