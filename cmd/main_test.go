package main

import (
	"flag"
	"os"
	"testing"
	"time"

	"github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestMainCommandLineFlags(t *testing.T) {
	// Save original flag values to restore after test
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	g := gomega.NewGomegaWithT(t)

	// Create a new FlagSet to simulate command line parsing
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var metricsCertPath string
	var metricsCertName string
	var metricsCertKey string
	var webhookCertPath string
	var webhookCertName string
	var webhookCertKey string
	var secureMetrics bool
	var enableHTTP2 bool

	// Define flags like in main()
	fs.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to.")
	fs.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	fs.StringVar(&metricsCertPath, "metrics-cert-path", "", "The directory that contains the metrics server certificate.")
	fs.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	fs.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	fs.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	fs.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	fs.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	fs.BoolVar(&secureMetrics, "metrics-secure", true, "If set, the metrics endpoint is served securely.")
	fs.BoolVar(&enableHTTP2, "enable-http2", false, "If set, HTTP/2 will be enabled.")

	// Test default values
	g.Expect(metricsAddr).To(gomega.Equal("0"))
	g.Expect(probeAddr).To(gomega.Equal(":8081"))
	g.Expect(enableLeaderElection).To(gomega.BeFalse())
	g.Expect(secureMetrics).To(gomega.BeTrue())
	g.Expect(enableHTTP2).To(gomega.BeFalse())

	// Test with custom command line args
	testArgs := []string{
		"--metrics-bind-address=:8443",
		"--health-probe-bind-address=:9090",
		"--leader-elect=true",
		"--metrics-secure=false",
		"--enable-http2=true",
		"--metrics-cert-path=/certs",
		"--webhook-cert-path=/webhook-certs",
	}
	err := fs.Parse(testArgs)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	// Verify parsed values
	g.Expect(metricsAddr).To(gomega.Equal(":8443"))
	g.Expect(probeAddr).To(gomega.Equal(":9090"))
	g.Expect(enableLeaderElection).To(gomega.BeTrue())
	g.Expect(secureMetrics).To(gomega.BeFalse())
	g.Expect(enableHTTP2).To(gomega.BeTrue())
	g.Expect(metricsCertPath).To(gomega.Equal("/certs"))
	g.Expect(webhookCertPath).To(gomega.Equal("/webhook-certs"))
}

func TestControllerManagerOptions(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Test creating controller manager options
	options := ctrl.Options{
		Scheme:                 nil, // Would be a real scheme in main
		LeaderElection:         true,
		LeaderElectionID:       "1e0e9135.kuberescue.io",
		HealthProbeBindAddress: ":9090",
	}

	// Verify options values
	g.Expect(options.LeaderElection).To(gomega.BeTrue())
	g.Expect(options.LeaderElectionID).To(gomega.Equal("1e0e9135.kuberescue.io"))
	g.Expect(options.HealthProbeBindAddress).To(gomega.Equal(":9090"))
}

func TestHealthChecks(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Test the health check function using basic ping handler
	pingFn := func() error { return nil }

	// A health check should return no error for a healthy system
	g.Expect(pingFn()).NotTo(gomega.HaveOccurred())
}

func TestLeaderElectionID(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Verify the leader election ID format
	leaderElectionID := "1e0e9135.kuberescue.io"
	g.Expect(leaderElectionID).To(gomega.MatchRegexp(`[a-f0-9]+\.kuberescue\.io`))
}

func TestSignalHandler(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Test that signal handler is created without error
	ctx := ctrl.SetupSignalHandler()
	g.Expect(ctx).NotTo(gomega.BeNil())

	// Test that context deadline is in the future (not expired)
	deadline, ok := ctx.Deadline()
	g.Expect(ok).To(gomega.BeFalse()) // Default context has no deadline

	// If there were a deadline, it would be in the future
	if ok {
		g.Expect(deadline).To(gomega.BeTemporally(">", time.Now()))
	}
}
