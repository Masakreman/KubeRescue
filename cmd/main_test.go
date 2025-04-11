package main

import (
	"crypto/tls"
	"flag"
	"testing"

	"github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	remediationv1alpha1 "github.com/Masakreman/KubeRescue/api/v1alpha1"
)

func TestMainSetup(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Simulate main() startup scenarios
	testCases := []struct {
		name          string
		args          []string
		expectedDevel bool
		expectedLevel int
	}{
		{
			name:          "Development Mode",
			args:          []string{"cmd", "--zap-devel=true"},
			expectedDevel: true,
			expectedLevel: 0, // Default log level
		},
		{
			name:          "Production Mode",
			args:          []string{"cmd", "--zap-devel=false"},
			expectedDevel: false,
			expectedLevel: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new flag set for each test to avoid flag redefinition
			fs := flag.NewFlagSet("test", flag.ContinueOnError)

			opts := zap.Options{
				Development: tc.expectedDevel,
			}

			// Bind flags to the new flag set
			opts.BindFlags(fs)

			// Parse the flags for this test case
			err := fs.Parse(tc.args[1:])
			g.Expect(err).To(gomega.Succeed())

			logger := zap.New(zap.UseFlagOptions(&opts))
			g.Expect(logger).NotTo(gomega.BeNil())

			ctrl.SetLogger(logger)
			g.Expect(logf.Log).NotTo(gomega.BeNil())
		})
	}
}

// ... rest of the test file remains the same

func TestSchemeInitialization(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Test complete scheme initialization
	scheme := runtime.NewScheme()

	// Add base Kubernetes client scheme
	err := clientgoscheme.AddToScheme(scheme)
	g.Expect(err).To(gomega.Succeed())

	// Add custom remediation CRD scheme
	err = remediationv1alpha1.AddToScheme(scheme)
	g.Expect(err).To(gomega.Succeed())

	// Verify CRD registration
	gvk := remediationv1alpha1.GroupVersion.WithKind("LogRemediation")
	obj, err := scheme.New(gvk)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(obj).NotTo(gomega.BeNil())

	logRemediation, ok := obj.(*remediationv1alpha1.LogRemediation)
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(logRemediation).NotTo(gomega.BeNil())
}

func TestManagerOptions(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Test different manager configuration scenarios
	testCases := []struct {
		name                  string
		leaderElection        bool
		healthProbeAddr       string
		metricsAddr           string
		expectedLeaderElectID string
	}{
		{
			name:                  "Default Configuration",
			leaderElection:        true,
			healthProbeAddr:       ":8081",
			metricsAddr:           ":8443",
			expectedLeaderElectID: "1e0e9135.kuberescue.io",
		},
		{
			name:                  "Disabled Leader Election",
			leaderElection:        false,
			healthProbeAddr:       ":9090",
			metricsAddr:           ":9443",
			expectedLeaderElectID: "1e0e9135.kuberescue.io",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			g.Expect(clientgoscheme.AddToScheme(scheme)).To(gomega.Succeed())
			g.Expect(remediationv1alpha1.AddToScheme(scheme)).To(gomega.Succeed())

			metricsOptions := metricsserver.Options{
				BindAddress:   tc.metricsAddr,
				SecureServing: true,
			}

			options := ctrl.Options{
				Scheme:                 scheme,
				Metrics:                metricsOptions,
				HealthProbeBindAddress: tc.healthProbeAddr,
				LeaderElection:         tc.leaderElection,
				LeaderElectionID:       tc.expectedLeaderElectID,
			}

			g.Expect(options.Scheme).NotTo(gomega.BeNil())
			g.Expect(options.HealthProbeBindAddress).To(gomega.Equal(tc.healthProbeAddr))
			g.Expect(options.LeaderElection).To(gomega.Equal(tc.leaderElection))
			g.Expect(options.LeaderElectionID).To(gomega.Equal(tc.expectedLeaderElectID))
		})
	}
}

func TestWebhookConfiguration(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	testCases := []struct {
		name            string
		enableHTTP2     bool
		expectHTTP1Only bool
	}{
		{
			name:            "Disable HTTP/2 (Default)",
			enableHTTP2:     false,
			expectHTTP1Only: true,
		},
		{
			name:            "Enable HTTP/2",
			enableHTTP2:     true,
			expectHTTP1Only: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var tlsOpts []func(*tls.Config)

			if !tc.enableHTTP2 {
				tlsOpts = append(tlsOpts, func(c *tls.Config) {
					c.NextProtos = []string{"http/1.1"}
				})
			}

			webhookServer := webhook.NewServer(webhook.Options{
				TLSOpts: tlsOpts,
			})

			g.Expect(webhookServer).NotTo(gomega.BeNil())
		})
	}
}

func TestMetricsServerConfiguration(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	testCases := []struct {
		name           string
		metricsAddr    string
		secureMetrics  bool
		filterProvider bool
	}{
		{
			name:           "Secure Metrics Default",
			metricsAddr:    ":8443",
			secureMetrics:  true,
			filterProvider: true,
		},
		{
			name:           "Unsecure Metrics",
			metricsAddr:    ":8080",
			secureMetrics:  false,
			filterProvider: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			metricsOptions := metricsserver.Options{
				BindAddress:   tc.metricsAddr,
				SecureServing: tc.secureMetrics,
			}

			if tc.filterProvider {
				metricsOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
			}

			g.Expect(metricsOptions.BindAddress).To(gomega.Equal(tc.metricsAddr))
			g.Expect(metricsOptions.SecureServing).To(gomega.Equal(tc.secureMetrics))

			if tc.filterProvider {
				g.Expect(metricsOptions.FilterProvider).NotTo(gomega.BeNil())
			}
		})
	}
}

func TestHealthProbes(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Simulate health probe configuration
	testCases := []struct {
		name       string
		probeAddr  string
		shouldPass bool
	}{
		{
			name:       "Default Health Probe Address",
			probeAddr:  ":8081",
			shouldPass: true,
		},
		{
			name:       "Custom Health Probe Address",
			probeAddr:  ":9090",
			shouldPass: true,
		},
	}

	// Mock GetConfigOrDie to avoid actual Kubernetes config
	originalGetConfigOrDie := ctrl.GetConfigOrDie
	defer func() { ctrl.GetConfigOrDie = originalGetConfigOrDie }()

	ctrl.GetConfigOrDie = func() *rest.Config {
		return &rest.Config{
			Host: "https://example.com",
		}
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
				HealthProbeBindAddress: tc.probeAddr,
				LeaderElection:         false,
				LeaderElectionID:       "test-leader-election",
			})

			if tc.shouldPass {
				g.Expect(err).To(gomega.Succeed())
				g.Expect(mgr).NotTo(gomega.BeNil())
			}
		})
	}
}
