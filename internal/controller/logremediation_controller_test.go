package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	remediationv1alpha1 "github.com/Masakreman/KubeRescue/api/v1alpha1"
)

func mockElasticsearchServer() *httptest.Server {
	handler := http.NewServeMux()

	//mock qury
	handler.HandleFunc("/kubernetes-logs/_search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"took": 5,
			"timed_out": false,
			"hits": {
				"total": {"value": 2, "relation": "eq"},
				"max_score": null,
				"hits": [
					{
						"_index": "kubernetes-logs",
						"_id": "1",
						"_source": {
							"@timestamp": "` + time.Now().Format(time.RFC3339) + `",
							"log": "CRITICAL_DB_CONNECTION_FAILED: Cannot connect to database",
							"kubernetes": {
								"pod_name": "test-pod-1",
								"namespace": "default",
								"labels": {
									"app": "test-app"
								}
							}
						}
					},
					{
						"_index": "kubernetes-logs",
						"_id": "2",
						"_source": {
							"@timestamp": "` + time.Now().Format(time.RFC3339) + `",
							"log": "Normal operation",
							"kubernetes": {
								"pod_name": "test-pod-2",
								"namespace": "default",
								"labels": {
									"app": "test-app"
								}
							}
						}
					}
				]
			}
		}`))
	})

	return httptest.NewServer(handler)
}

var _ = Describe("LogRemediation Controller", func() {
	const (
		resourceName      = "test-remediation"
		resourceNamespace = "default"
		timeout           = time.Second * 10
		interval          = time.Millisecond * 250
	)

	Context("for testing reconciliation", func() {
		var (
			ctx            context.Context
			mockServer     *httptest.Server
			fakeClient     client.Client
			k8sObjects     []runtime.Object
			reconciler     *LogRemediationReconciler
			req            reconcile.Request
			logremediation *remediationv1alpha1.LogRemediation
			deploymentName = "test-deployment"
			podName        = "test-pod-1"
		)

		BeforeEach(func() {
			ctx = context.Background()
			mockServer = mockElasticsearchServer()

			// create custom resource
			logremediation = &remediationv1alpha1.LogRemediation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: resourceNamespace,
				},
				Spec: remediationv1alpha1.LogRemediationSpec{
					Sources: []remediationv1alpha1.LogSource{
						{
							Type: "deployment",
							Selector: map[string]string{
								"app": "test-app",
							},
						},
					},
					ElasticsearchConfig: remediationv1alpha1.ElasticsearchConfig{
						Host:  mockServer.URL,
						Port:  9200,
						Index: "kubernetes-logs",
					},
					RemediationRules: []remediationv1alpha1.RemediationRule{
						{
							ErrorPattern:   "CRITICAL_DB_CONNECTION_FAILED",
							Action:         "restart",
							CooldownPeriod: 60,
						},
						{
							ErrorPattern:   "HIGH_MEMORY_USAGE",
							Action:         "scale",
							CooldownPeriod: 120,
						},
					},
				},
				Status: remediationv1alpha1.LogRemediationStatus{
					ObservedGeneration: 1,
					Conditions: []metav1.Condition{
						{
							Type:               "Ready",
							Status:             metav1.ConditionTrue,
							LastTransitionTime: metav1.Now(),
							Reason:             "Reconciled",
							Message:            "Successfully reconciled",
						},
					},
				},
			}

			//test pod/app
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: resourceNamespace,
					Labels: map[string]string{
						"app": "test-app",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "ReplicaSet",
							Name:       "test-replicaset",
							UID:        "123",
							Controller: boolPtr(true),
						},
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "test-image",
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			}

			//test replicaset
			replicaSet := &appsv1.ReplicaSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-replicaset",
					Namespace: resourceNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       deploymentName,
							UID:        "67890",
							Controller: boolPtr(true),
						},
					},
				},
				Spec: appsv1.ReplicaSetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "test-app",
						},
					},
				},
			}

			//test deployment
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deploymentName,
					Namespace: resourceNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "test-app",
						},
					},
					Replicas: int32Ptr(1),
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "test-app",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "test-container",
									Image: "test-image",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("50m"),
											corev1.ResourceMemory: resource.MustParse("50Mi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("150m"),
											corev1.ResourceMemory: resource.MustParse("150Mi"),
										},
									},
								},
							},
						},
					},
				},
			}

			// objects for creation
			k8sObjects = []runtime.Object{
				logremediation,
				pod,
				replicaSet,
				deployment,
			}
			scheme := runtime.NewScheme()
			Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
			Expect(appsv1.AddToScheme(scheme)).To(Succeed())
			Expect(remediationv1alpha1.AddToScheme(scheme)).To(Succeed())

			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(k8sObjects...).
				WithStatusSubresource(&remediationv1alpha1.LogRemediation{}).
				Build()

			reconciler = &LogRemediationReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			req = reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: resourceNamespace,
				},
			}
		})

		AfterEach(func() {
			mockServer.Close()
		})

		It("should successfully reconcile the LogRemediation resource", func() {
			By("Reconciling the LogRemediation resource")

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Minute * 2))
			By("Checking if the ConfigMap was created")
			configMap := &corev1.ConfigMap{}
			err = fakeClient.Get(ctx, types.NamespacedName{
				Name:      resourceName + "-fluentbit-config",
				Namespace: resourceNamespace,
			}, configMap)
			Expect(err).NotTo(HaveOccurred())
			Expect(configMap.Data).To(HaveKey("fluent-bit.conf"))
			Expect(configMap.Data).To(HaveKey("parsers.conf"))
		})

		It("should handle resource not found during reconciliation", func() {
			By("Deleting the LogRemediation resource before reconciliation")
			err := fakeClient.Delete(ctx, logremediation)
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling a non-existent resource")
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})

		It("should create a DaemonSet for Fluentbit", func() {
			By("Reconciling the LogRemediation resource")
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Minute * 2))

			By("Checking if the DaemonSet is created")
			daemonSet := &appsv1.DaemonSet{}
			err = fakeClient.Get(ctx, types.NamespacedName{
				Name:      resourceName + "-fluentbit",
				Namespace: resourceNamespace,
			}, daemonSet)
			Expect(err).NotTo(HaveOccurred())
			Expect(daemonSet.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(daemonSet.Spec.Template.Spec.Containers[0].Name).To(Equal("fluentbit"))
		})

		It("should properly generate Fluentbit configuration", func() {
			By("Getting the generated Fluentbit config")
			config := reconciler.generateFluentbitConfig(logremediation)

			By("Checking if the config contains the required components")
			Expect(config).To(ContainSubstring("[SERVICE]"))
			Expect(config).To(ContainSubstring("[INPUT]"))
			Expect(config).To(ContainSubstring("[FILTER]"))
			Expect(config).To(ContainSubstring("[OUTPUT]"))

			By("Checking if the buffer size is applied")
			logremediation.Spec.FluentbitConfig = &remediationv1alpha1.FluentbitConfig{
				BufferSize:    "200MB",
				FlushInterval: 5,
			}
			config = reconciler.generateFluentbitConfig(logremediation)
			Expect(config).To(ContainSubstring("Buffer_Size  200MB"))
			Expect(config).To(ContainSubstring("Flush        5"))
		})

		It("should handle finaliser logic", func() {
			logRemediationWithFinalizer := &remediationv1alpha1.LogRemediation{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-with-finalser",
					Namespace:  resourceNamespace,
					Finalizers: []string{"kuberescue.io/finalizer"},
				},
				Spec: logremediation.Spec,
			}
			Expect(fakeClient.Create(ctx, logRemediationWithFinalizer)).To(Succeed())

			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-with-finalizer-fluentbit-config",
					Namespace: resourceNamespace,
				},
				Data: map[string]string{
					"fluent-bit.conf": "test config",
					"parsers.conf":    "test parsers",
				},
			}
			Expect(fakeClient.Create(ctx, configMap)).To(Succeed())

			daemonSet := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-with-finaliser-fluentbit",
					Namespace: resourceNamespace,
				},
				Spec: appsv1.DaemonSetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "test-app",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "test-app",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "fluentbit",
									Image: "test-image",
								},
							},
						},
					},
				},
			}
			Expect(fakeClient.Create(ctx, daemonSet)).To(Succeed())

			updatedLogRemediation := &remediationv1alpha1.LogRemediation{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{
				Name:      "test-with-finaliser",
				Namespace: resourceNamespace,
			}, updatedLogRemediation)).To(Succeed())

			now := metav1.Now()
			updatedLogRemediation.ObjectMeta.DeletionTimestamp = &now
			scheme := runtime.NewScheme()
			Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
			Expect(appsv1.AddToScheme(scheme)).To(Succeed())
			Expect(remediationv1alpha1.AddToScheme(scheme)).To(Succeed())

			updatedObjects := []runtime.Object{
				updatedLogRemediation,
				configMap,
				daemonSet,
			}

			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(updatedObjects...).
				WithStatusSubresource(&remediationv1alpha1.LogRemediation{}).
				Build()

			reconciler.Client = fakeClient

			req = reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-with-finaliser",
					Namespace: resourceNamespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())

			finalResource := &remediationv1alpha1.LogRemediation{}
			err = fakeClient.Get(ctx, types.NamespacedName{
				Name:      "test-with-finaliser",
				Namespace: resourceNamespace,
			}, finalResource)

			if !errors.IsNotFound(err) {
				Expect(finalResource.ObjectMeta.Finalizers).To(BeEmpty())
			}
		})
	})
})

// helper functions
func boolPtr(b bool) *bool {
	return &b
}

func int32Ptr(i int32) *int32 {
	return &i
}
