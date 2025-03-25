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

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	remediationv1alpha1 "github.com/Masakreman/KubeRescue/api/v1alpha1"
)

// LogRemediationReconciler reconciles a LogRemediation object
type LogRemediationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=remediation.kuberescue.io,resources=logremediations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=remediation.kuberescue.io,resources=logremediations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=remediation.kuberescue.io,resources=logremediations/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile handles the main reconciliation loop for LogRemediation
func (r *LogRemediationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling LogRemediation", "name", req.Name, "namespace", req.Namespace)

	// Fetch the LogRemediation instance
	logRemediation := &remediationv1alpha1.LogRemediation{}
	err := r.Get(ctx, req.NamespacedName, logRemediation)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted
			logger.Info("LogRemediation resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request
		logger.Error(err, "Failed to get LogRemediation")
		return ctrl.Result{}, err
	}

	// Handle finalizers and deletion
	finalizerName := "kuberescue.io/finalizer"
	if logRemediation.ObjectMeta.DeletionTimestamp.IsZero() {
		// Resource is not being deleted, ensure it has our finalizer
		if !containsString(logRemediation.ObjectMeta.Finalizers, finalizerName) {
			logRemediation.ObjectMeta.Finalizers = append(logRemediation.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(ctx, logRemediation); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// Resource is being deleted
		if containsString(logRemediation.ObjectMeta.Finalizers, finalizerName) {
			// Run finalization logic
			if err := r.finalizeLogRemediation(ctx, logRemediation); err != nil {
				return ctrl.Result{}, err
			}

			// Remove finalizer
			logRemediation.ObjectMeta.Finalizers = removeString(logRemediation.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(ctx, logRemediation); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Create or update ConfigMap for Fluentbit configuration
	if err := r.reconcileFluentbitConfigMap(ctx, logRemediation); err != nil {
		logger.Error(err, "Failed to reconcile Fluentbit ConfigMap")
		r.updateLogRemediationStatus(ctx, logRemediation, "ConfigMapFailed", "Failed to create or update Fluentbit ConfigMap", metav1.ConditionFalse)
		return ctrl.Result{RequeueAfter: time.Second * 10}, err
	}

	// Create or update DaemonSet for Fluentbit
	if err := r.reconcileFluentbitDaemonSet(ctx, logRemediation); err != nil {
		logger.Error(err, "Failed to reconcile Fluentbit DaemonSet")
		r.updateLogRemediationStatus(ctx, logRemediation, "DaemonSetFailed", "Failed to create or update Fluentbit DaemonSet", metav1.ConditionFalse)
		return ctrl.Result{RequeueAfter: time.Second * 10}, err
	}

	// Update pod status
	if err := r.updatePodStatus(ctx, logRemediation); err != nil {
		logger.Error(err, "Failed to update pod status")
		return ctrl.Result{RequeueAfter: time.Second * 10}, err
	}

	// Update successful status
	r.updateLogRemediationStatus(ctx, logRemediation, "Reconciled", "Successfully reconciled LogRemediation", metav1.ConditionTrue)

	// Check for errors and remediate if remediation rules exist
	if len(logRemediation.Spec.RemediationRules) > 0 {
		if err := r.checkLogsAndRemediate(ctx, logRemediation); err != nil {
			logger.Error(err, "Failed to check logs and remediate")
			// Don't return error here, just log it
		}
	}

	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}

// finalizeLogRemediation handles cleanup when a LogRemediation resource is deleted
func (r *LogRemediationReconciler) finalizeLogRemediation(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	logger := log.FromContext(ctx)
	logger.Info("Finalizing LogRemediation", "name", lr.Name, "namespace", lr.Namespace)

	// Delete the DaemonSet if it exists
	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-fluentbit", lr.Name),
			Namespace: lr.Namespace,
		},
	}
	if err := r.Delete(ctx, daemonSet); err != nil && !errors.IsNotFound(err) {
		return err
	}

	// Delete the ConfigMap if it exists
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-fluentbit-config", lr.Name),
			Namespace: lr.Namespace,
		},
	}
	if err := r.Delete(ctx, configMap); err != nil && !errors.IsNotFound(err) {
		return err
	}

	logger.Info("Successfully finalized LogRemediation")
	return nil
}

// generateFluentbitConfig creates a Fluentbit configuration based on the LogRemediation spec

func (r *LogRemediationReconciler) generateFluentbitConfig(lr *remediationv1alpha1.LogRemediation) string {
	// Create a basic service section
	config := `[SERVICE]
    Flush        5
    Daemon       Off
    Log_Level    info
    Parsers_File parsers.conf
    HTTP_Server  On
    HTTP_Listen  0.0.0.0
    HTTP_Port    2020
    Buffer_Size  5MB

`

	// Override with custom settings if provided
	if lr.Spec.FluentbitConfig != nil {
		if lr.Spec.FluentbitConfig.BufferSize != "" {
			config = fmt.Sprintf(
				"[SERVICE]\n    Flush        %d\n    Daemon       Off\n    Log_Level    info\n    Parsers_File parsers.conf\n    HTTP_Server  On\n    HTTP_Listen  0.0.0.0\n    HTTP_Port    2020\n    Buffer_Size  %s\n\n",
				lr.Spec.FluentbitConfig.FlushInterval,
				lr.Spec.FluentbitConfig.BufferSize,
			)
		}
	}

	// Standard input for container logs
	config += `[INPUT]
    Name             tail
    Tag              kube.*
    Path             /var/log/containers/*.log
    Parser           docker
    DB               /var/log/flb_kube.db
    Mem_Buf_Limit    5MB
    Skip_Long_Lines  On
    Refresh_Interval 10

`

	// Add Kubernetes filter
	config += `[FILTER]
    Name                kubernetes
    Match               kube.*
    Kube_URL            https://kubernetes.default.svc:443
    Kube_CA_File        /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
    Kube_Token_File     /var/run/secrets/kubernetes.io/serviceaccount/token
    Kube_Tag_Prefix     kube.var.log.containers.
    Merge_Log           On
    K8S-Logging.Parser  On
    K8S-Logging.Exclude Off

`

	// Apply source filters based on LogRemediation sources
	for i, source := range lr.Spec.Sources {
		filterName := fmt.Sprintf("source_filter_%d", i)

		switch source.Type {
		case "pod":
			if podName, ok := source.Selector["name"]; ok {
				config += fmt.Sprintf(`[FILTER]
    Name                grep
    Match               kube.*
	Filter ID: 			%s
    Regex               kubernetes.pod_name %s

`, filterName, podName)
			}
		case "namespace":
			if namespace, ok := source.Selector["name"]; ok {
				config += fmt.Sprintf(`[FILTER]
    Name                grep
    Match               kube.*
    Regex               kubernetes.namespace_name %s

`, namespace)
			}
		case "deployment":
			if deployment, ok := source.Selector["name"]; ok {
				config += fmt.Sprintf(`[FILTER]
    Name                grep
    Match               kube.*
    Regex               kubernetes.labels.app %s

`, deployment)
			}
		}
	}

	// Add Elasticsearch output
	esConfig := lr.Spec.ElasticsearchConfig
	config += fmt.Sprintf(`[OUTPUT]
    Name            es
    Match           kube.*
    Host            %s
    Port            %d
    Index           %s
    Generate_ID     On
    Replace_Dots    On
    Logstash_Format Off
`, esConfig.Host, esConfig.Port, esConfig.Index)

	// Add auth if specified
	if esConfig.SecretRef != "" {
		config += `    HTTP_User        ${ES_USER}
    HTTP_Passwd      ${ES_PASSWORD}
    tls              On
    tls.verify       Off
`
	}

	return config
}

// reconcileFluentbitConfigMap ensures the ConfigMap exists with the correct configuration
func (r *LogRemediationReconciler) reconcileFluentbitConfigMap(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	logger := log.FromContext(ctx)

	// Generate Fluentbit configuration
	fbConfig := r.generateFluentbitConfig(lr)

	// Define basic parsers config
	parsersConfig := `[PARSER]
    Name   docker
    Format json
    Time_Key time
    Time_Format %Y-%m-%dT%H:%M:%S.%L
    Time_Keep On
`

	// Create ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-fluentbit-config", lr.Name),
			Namespace: lr.Namespace,
		},
		Data: map[string]string{
			"fluent-bit.conf": fbConfig,
			"parsers.conf":    parsersConfig,
		},
	}

	// Set controller reference
	if err := controllerutil.SetControllerReference(lr, configMap, r.Scheme); err != nil {
		return err
	}

	// Create or update ConfigMap
	found := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating Fluentbit ConfigMap", "name", configMap.Name)
		return r.Create(ctx, configMap)
	} else if err != nil {
		return err
	}

	// Update if configuration changed
	if found.Data["fluent-bit.conf"] != configMap.Data["fluent-bit.conf"] {
		logger.Info("Updating Fluentbit ConfigMap", "name", configMap.Name)
		found.Data = configMap.Data
		return r.Update(ctx, found)
	}

	return nil
}

// reconcileFluentbitDaemonSet ensures the DaemonSet exists with the correct configuration
func (r *LogRemediationReconciler) reconcileFluentbitDaemonSet(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	logger := log.FromContext(ctx)

	// Create labels for resources
	labels := map[string]string{
		"app":        fmt.Sprintf("%s-fluentbit", lr.Name),
		"controller": lr.Name,
	}

	// Create DaemonSet
	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-fluentbit", lr.Name),
			Namespace: lr.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "default", // Using the operator's service account
					Containers: []corev1.Container{
						{
							Name:  "fluentbit",
							Image: "fluent/fluent-bit:1.9",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(200*1024*1024, resource.BinarySI),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewMilliQuantity(50, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(100*1024*1024, resource.BinarySI),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "config",
									MountPath: "/fluent-bit/etc/",
								},
								{
									Name:      "varlog",
									MountPath: "/var/log",
								},
								{
									Name:      "varlibdockercontainers",
									MountPath: "/var/lib/docker/containers",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("%s-fluentbit-config", lr.Name),
									},
								},
							},
						},
						{
							Name: "varlog",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/log",
								},
							},
						},
						{
							Name: "varlibdockercontainers",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/docker/containers",
								},
							},
						},
					},
				},
			},
		},
	}

	// Add environment variables for Elasticsearch authentication if needed
	if lr.Spec.ElasticsearchConfig.SecretRef != "" {
		daemonSet.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
			{
				Name: "ES_USER",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: lr.Spec.ElasticsearchConfig.SecretRef,
						},
						Key: "username",
					},
				},
			},
			{
				Name: "ES_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: lr.Spec.ElasticsearchConfig.SecretRef,
						},
						Key: "password",
					},
				},
			},
		}
	}

	// Set controller reference
	if err := controllerutil.SetControllerReference(lr, daemonSet, r.Scheme); err != nil {
		return err
	}

	// Create or update DaemonSet
	found := &appsv1.DaemonSet{}
	err := r.Get(ctx, types.NamespacedName{Name: daemonSet.Name, Namespace: daemonSet.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating Fluentbit DaemonSet", "name", daemonSet.Name)
		return r.Create(ctx, daemonSet)
	} else if err != nil {
		return err
	}

	// Update if template spec changed
	if !reflect.DeepEqual(found.Spec.Template.Spec, daemonSet.Spec.Template.Spec) {
		logger.Info("Updating Fluentbit DaemonSet", "name", daemonSet.Name)
		found.Spec = daemonSet.Spec
		return r.Update(ctx, found)
	}

	return nil
}

// updatePodStatus updates the status section with information about the running Fluentbit pods
func (r *LogRemediationReconciler) updatePodStatus(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	// List pods matching our label
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(lr.Namespace),
		client.MatchingLabels(map[string]string{"app": fmt.Sprintf("%s-fluentbit", lr.Name)}),
	}

	if err := r.List(ctx, podList, listOpts...); err != nil {
		return err
	}

	// Extract pod names and update status
	var podNames []string
	for _, pod := range podList.Items {
		podNames = append(podNames, pod.Name)
	}

	// Update status
	lr.Status.FluentbitPods = podNames
	lr.Status.LastConfigured = &metav1.Time{Time: time.Now()}

	return r.Status().Update(ctx, lr)
}

// updateLogRemediationStatus updates the status condition for the LogRemediation resource
func (r *LogRemediationReconciler) updateLogRemediationStatus(ctx context.Context, lr *remediationv1alpha1.LogRemediation, reason, message string, status metav1.ConditionStatus) error {
	// Find or create the Ready condition
	meta.SetStatusCondition(&lr.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		ObservedGeneration: lr.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	})

	return r.Status().Update(ctx, lr)
}

// Helper functions for finalizers
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// SetupWithManager sets up the controller with the Manager.
func (r *LogRemediationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&remediationv1alpha1.LogRemediation{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}

// Check for errors and Perform remediation if required
func (r *LogRemediationReconciler) checkLogsAndRemediate(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	logger := log.FromContext(ctx)

	// Skip if no remediation rules defined
	if len(lr.Spec.RemediationRules) == 0 {
		return nil
	}

	// Get Elasticsearch endpoint
	esEndpoint := fmt.Sprintf("http://%s:%d/%s/_search",
		lr.Spec.ElasticsearchConfig.Host,
		lr.Spec.ElasticsearchConfig.Port,
		lr.Spec.ElasticsearchConfig.Index)

	// Simplify query for testing
	query := `{
        "query": { "match_all": {} },
        "size": 10
    }`

	// Query Elasticsearch
	req, err := http.NewRequest("POST", esEndpoint, strings.NewReader(query))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error(err, "Failed to query Elasticsearch")
		return err
	}
	defer resp.Body.Close()

	// Read response
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	logger.Info("Elasticsearch response status", "status", resp.Status)
	logger.Info("Elasticsearch response body", "body", string(body))

	// Parse response
	var esResult map[string]interface{}
	if err := json.Unmarshal(body, &esResult); err != nil {
		logger.Error(err, "Failed to parse Elasticsearch response", "body", string(body))
		return err
	}

	// Log all keys in the response
	logger.Info("Elasticsearch response keys", "keys", fmt.Sprintf("%v", reflect.ValueOf(esResult).MapKeys()))

	// Extract hits
	hits, ok := esResult["hits"]
	if !ok {
		logger.Error(nil, "Missing 'hits' field in response", "response", esResult)
		return fmt.Errorf("missing 'hits' field in Elasticsearch response")
	}

	// Convert hits to map
	hitsMap, ok := hits.(map[string]interface{})
	if !ok {
		logger.Error(nil, "Expected 'hits' to be a map", "hits_type", fmt.Sprintf("%T", hits))
		return fmt.Errorf("invalid 'hits' field type in Elasticsearch response")
	}

	// Log keys in the hits map
	logger.Info("Hits map keys", "keys", fmt.Sprintf("%v", reflect.ValueOf(hitsMap).MapKeys()))

	// Extract hits list
	hitsList, ok := hitsMap["hits"]
	if !ok {
		logger.Error(nil, "Missing 'hits.hits' field in response", "hits", hitsMap)
		return fmt.Errorf("missing 'hits.hits' field in Elasticsearch response")
	}

	// Convert hits list to array
	hitsArray, ok := hitsList.([]interface{})
	if !ok {
		logger.Error(nil, "Expected 'hits.hits' to be an array", "hits_list_type", fmt.Sprintf("%T", hitsList))
		return fmt.Errorf("invalid 'hits.hits' field type in Elasticsearch response")
	}

	// Log the number of hits
	logger.Info("Found hits", "count", len(hitsArray))

	// Check each log against remediation rules
	for i, hitObj := range hitsArray {
		logger.Info("Processing hit", "index", i)

		hitMap, ok := hitObj.(map[string]interface{})
		if !ok {
			logger.Error(nil, "Hit is not a map", "hit_type", fmt.Sprintf("%T", hitObj))
			continue
		}

		source, ok := hitMap["_source"].(map[string]interface{})
		if !ok {
			logger.Error(nil, "Hit source is not a map", "source_type", fmt.Sprintf("%T", hitMap["_source"]))
			continue
		}

		// Log the source keys
		logger.Info("Source keys", "keys", fmt.Sprintf("%v", reflect.ValueOf(source).MapKeys()))

		// Get log message safely
		logMsgRaw, hasLog := source["log"]
		if !hasLog {
			logger.Error(nil, "No log field in source", "source", source)
			continue
		}

		logMsg, ok := logMsgRaw.(string)
		if !ok {
			logger.Error(nil, "Log field is not a string", "log_type", fmt.Sprintf("%T", logMsgRaw))
			continue
		}

		// Check for kubernetes metadata safely
		k8sRaw, hasK8s := source["kubernetes"]
		if !hasK8s {
			logger.Error(nil, "No kubernetes field in source", "source", source)
			continue
		}

		k8s, ok := k8sRaw.(map[string]interface{})
		if !ok {
			logger.Error(nil, "Kubernetes field is not a map", "k8s_type", fmt.Sprintf("%T", k8sRaw))
			continue
		}

		// Extract pod and namespace
		podNameRaw, hasPod := k8s["pod_name"]
		namespaceRaw, hasNamespace := k8s["namespace_name"]

		if !hasPod || !hasNamespace {
			logger.Error(nil, "Missing pod_name or namespace_name", "k8s", k8s)
			continue
		}

		podName, ok1 := podNameRaw.(string)
		namespace, ok2 := namespaceRaw.(string)

		if !ok1 || !ok2 {
			logger.Error(nil, "pod_name or namespace_name is not a string",
				"pod_name_type", fmt.Sprintf("%T", podNameRaw),
				"namespace_type", fmt.Sprintf("%T", namespaceRaw))
			continue
		}

		// Check against rules
		for _, rule := range lr.Spec.RemediationRules {
			matched, err := regexp.MatchString(rule.ErrorPattern, logMsg)
			if err != nil {
				logger.Error(err, "Error matching pattern", "pattern", rule.ErrorPattern)
				continue
			}

			if matched {
				logger.Info("Error pattern matched", "pattern", rule.ErrorPattern, "pod", podName)

				// Perform remediation based on action type
				switch rule.Action {
				case "restart":
					if err := r.restartPod(ctx, namespace, podName); err != nil {
						logger.Error(err, "Failed to restart pod", "pod", podName)
						continue
					}
					logger.Info("Pod restarted successfully", "pod", podName)

					// Store remediation action in status
					r.recordRemediationAction(ctx, lr, podName, rule.ErrorPattern, "restart")

					// We've taken an action, so return
					return nil
				}
			}
		}
	}

	return nil
}

// Function to restart a pod
func (r *LogRemediationReconciler) restartPod(ctx context.Context, namespace, podName string) error {

	// Get the pod
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: namespace}, pod); err != nil {
		return err
	}

	// Delete the pod (it will be recreated by the deployment controller)
	return r.Delete(ctx, pod)
}

// Record the remediation actions in the CR status
func (r *LogRemediationReconciler) recordRemediationAction(ctx context.Context, lr *remediationv1alpha1.LogRemediation, podName, pattern, action string) error {

	// Add a new entry to the RemediationHistory in status
	entry := remediationv1alpha1.RemediationHistoryEntry{
		Timestamp: metav1.Now(),
		PodName:   podName,
		Pattern:   pattern,
		Action:    action,
	}

	lr.Status.RemediationHistory = append(lr.Status.RemediationHistory, entry)

	// Update the status
	return r.Status().Update(ctx, lr)
}
