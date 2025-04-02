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
	"math"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	remediationv1alpha1 "github.com/Masakreman/KubeRescue/api/v1alpha1"
	"github.com/Masakreman/KubeRescue/internal/metrics"
)

// LogRemediationReconciler reconciles a LogRemediation object
type LogRemediationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=remediation.kuberescue.io,resources=logremediations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=remediation.kuberescue.io,resources=logremediations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=remediation.kuberescue.io,resources=logremediations/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;delete

//+kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;patch;update;watch
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;patch;update;watch
//+kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch

// Reconcile handles the main reconciliation loop for LogRemediation
func (r *LogRemediationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling LogRemediation", "name", req.Name, "namespace", req.Namespace)

	// Fetch the LogRemediation instance
	logRemediation := &remediationv1alpha1.LogRemediation{}
	err := r.Get(ctx, req.NamespacedName, logRemediation)
	if err != nil {
		if errors.IsNotFound(err) {
			// Resource is gone, decrease active count
			metrics.ActiveRemediations.WithLabelValues(req.Namespace).Dec()
			// Request object not found, could have been deleted
			logger.Info("LogRemediation resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request
		logger.Error(err, "Failed to get LogRemediation")
		return ctrl.Result{}, err
	}

	// If this is a new resource, increment the active count
	if logRemediation.Status.LastConfigured == nil {
		metrics.ActiveRemediations.WithLabelValues(req.Namespace).Inc()
	}

	// Now that we have the object, check if resources need to be reconciled
	// Only skip resource creation/update if recent and unchanged, but always check logs
	var skipResourceReconciliation bool

	if logRemediation.Status.LastConfigured != nil {
		lastReconciled := logRemediation.Status.LastConfigured.Time
		// If we reconciled resources in the last 2 minutes and no spec change
		if time.Since(lastReconciled) < time.Minute*2 &&
			logRemediation.Generation == logRemediation.Status.ObservedGeneration {
			logger.Info("Skipping resource reconciliation, checking logs only")
			skipResourceReconciliation = true
		}
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

	// Only reconcile resources if needed
	if !skipResourceReconciliation {
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
	}

	// Always check for errors and remediate if remediation rules exist
	if len(logRemediation.Spec.RemediationRules) > 0 {
		logger.Info("Checking logs for remediation", "rules_count", len(logRemediation.Spec.RemediationRules))
		if err := r.checkLogsAndRemediate(ctx, logRemediation); err != nil {
			logger.Error(err, "Failed to check logs and remediate")
			// Don't return error here, just log it
		}
	}

	// Requeue more frequently to check for errors to remediate
	return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
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

// generate configuration for Fluentibit with fixes for multiple instances
func (r *LogRemediationReconciler) generateFluentbitConfig(lr *remediationv1alpha1.LogRemediation) string {
	// Create a unique identifier for this instance
	instanceID := lr.Name

	// Create a basic service section
	config := `[SERVICE]
    Flush        1
    Daemon       Off
    Log_Level    debug
    Parsers_File parsers.conf
    HTTP_Server  On
    HTTP_Listen  0.0.0.0
    HTTP_Port    2020
    Buffer_Size  5MB

`

	// Apply custom settings if provided
	if lr.Spec.FluentbitConfig != nil {
		if lr.Spec.FluentbitConfig.BufferSize != "" {
			config = strings.Replace(config, "Buffer_Size  5MB", fmt.Sprintf("Buffer_Size  %s", lr.Spec.FluentbitConfig.BufferSize), 1)
		}
		if lr.Spec.FluentbitConfig.FlushInterval != 0 {
			config = strings.Replace(config, "Flush        1", fmt.Sprintf("Flush        %d", lr.Spec.FluentbitConfig.FlushInterval), 1)
		}
	}

	// Build a path pattern that focuses on test applications
	pathPattern := "/var/log/containers/test*_*_*.log" // Will match testApps: test1, test2, test3, etc.

	// Input configuration with fixed tag format
	config += fmt.Sprintf(`[INPUT]
    Name            tail
    Path            %s
    Exclude_Path    /var/log/containers/*fluentbit*.log
    Parser          docker
    Tag             kube.*
    Refresh_Interval 1
    Mem_Buf_Limit   5MB
    Skip_Long_Lines On
    DB              /var/lib/fluent-bit/%s.db
    Read_from_Head  True
    Ignore_Older    5m  # Don't ignore older logs initially
    Exit_On_Eof     false

`, pathPattern, instanceID)

	// Add Kubernetes metadata filter with correct tag matching
	config += `[FILTER]
    Name                kubernetes
    Match               kube.*
    Kube_URL            https://kubernetes.default.svc:443
    Kube_CA_File        /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
    Kube_Token_File     /var/run/secrets/kubernetes.io/serviceaccount/token
    Merge_Log           On
    Merge_Log_Key       log_processed
    K8S-Logging.Parser  On
    K8S-Logging.Exclude Off

`

	// No error pattern filtering here - we send everything to Elasticsearch
	// and filter at query time

	// Add a stdout output for debugging
	config += `[OUTPUT]
    Name            stdout
    Match           kube.*
    Format          json_lines

`

	// Add Elasticsearch output
	esConfig := lr.Spec.ElasticsearchConfig
	hostName := esConfig.Host
	// Ensure we use the FQDN if a short name is provided
	if !strings.Contains(hostName, ".") {
		hostName = fmt.Sprintf("%s.%s.svc.cluster.local", hostName, lr.Namespace)
	}

	config += fmt.Sprintf(`[OUTPUT]
    Name               es
    Match              kube.*
    Host               %s
    Port               %d
    Index              %s
    Generate_ID        On
    Suppress_Type_Name On
    HTTP_User          elastic
    HTTP_Passwd        changeme
    Trace_Output       On
    Trace_Error        On
`, hostName, esConfig.Port, esConfig.Index)

	return config
}

// reconcileFluentbitConfigMap ensures the ConfigMap exists with the correct configuration
func (r *LogRemediationReconciler) reconcileFluentbitConfigMap(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	logger := log.FromContext(ctx)

	// Generate Fluentbit configuration
	fbConfig := r.generateFluentbitConfig(lr)

	// Define parsers config in a separate variable
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
	if found.Data["fluent-bit.conf"] != configMap.Data["fluent-bit.conf"] ||
		found.Data["parsers.conf"] != configMap.Data["parsers.conf"] {
		logger.Info("Updating Fluentbit ConfigMap", "name", configMap.Name)
		found.Data = configMap.Data
		return r.Update(ctx, found)
	}

	return nil
}

func (r *LogRemediationReconciler) reconcileFluentbitDaemonSet(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	logger := log.FromContext(ctx)

	// Create a hash based on the configuration content rather than time
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      fmt.Sprintf("%s-fluentbit-config", lr.Name),
		Namespace: lr.Namespace,
	}, configMap)

	// Default hash if we can't get the ConfigMap
	configHash := fmt.Sprintf("%s-default", lr.Name)

	if err == nil {
		// Generate hash based on the ConfigMap data
		configContent := configMap.Data["fluent-bit.conf"]
		// Simple hash - in production you might want a more robust hash function
		configHash = fmt.Sprintf("%s-%d", lr.Name, len(configContent))
	}

	// Create labels for resources
	labels := map[string]string{
		"app":        fmt.Sprintf("%s-fluentbit", lr.Name),
		"controller": lr.Name,
	}

	// Create DaemonSet with improved configuration
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
					Annotations: map[string]string{
						// Add hash annotation to force recreation when config changes
						"kuberescue.io/config-hash": configHash,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "default",
					Containers: []corev1.Container{
						{
							Name:  "fluentbit",
							Image: "fluent/fluent-bit:2.1.10", // Using a stable version
							Env: []corev1.EnvVar{
								{
									Name:  "ES_USER",
									Value: "elastic",
								},
								{
									Name:  "ES_PASSWORD",
									Value: "changeme",
								},
								// Add environment variables for unique instance ID
								{
									Name:  "FLUENT_INSTANCE",
									Value: lr.Name,
								},
								{
									Name:  "FLUENT_DB_RESET",
									Value: configHash, // Use hash to reset DB
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewMilliQuantity(200, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(300*1024*1024, resource.BinarySI),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(150*1024*1024, resource.BinarySI),
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
								{
									// Store DB in a dedicated volume with instance-specific path
									Name:      "flb-state",
									MountPath: "/var/lib/fluent-bit",
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/api/v1/health",
										Port: intstr.FromInt(2020),
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
								SuccessThreshold:    1,
								FailureThreshold:    3,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/",
										Port: intstr.FromInt(2020),
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       30,
								TimeoutSeconds:      5,
								SuccessThreshold:    1,
								FailureThreshold:    3,
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
						{
							Name: "flb-state",
							VolumeSource: corev1.VolumeSource{
								// Use an EmptyDir volume with unique hash to avoid conflicts
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
					Tolerations: []corev1.Toleration{
						{
							Effect:   corev1.TaintEffectNoSchedule,
							Operator: corev1.TolerationOpExists,
						},
					},
				},
			},
		},
	}

	// Add environment variables for Elasticsearch authentication if needed
	if lr.Spec.ElasticsearchConfig.SecretRef != "" {
		// Create new env var slice preserving the existing vars
		envVars := []corev1.EnvVar{
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
			// Preserve the instance ID and DB reset
			{
				Name:  "FLUENT_INSTANCE",
				Value: lr.Name,
			},
			{
				Name:  "FLUENT_DB_RESET",
				Value: configHash,
			},
		}
		daemonSet.Spec.Template.Spec.Containers[0].Env = envVars
	}

	// Set controller reference
	if err := controllerutil.SetControllerReference(lr, daemonSet, r.Scheme); err != nil {
		return err
	}

	// Create or update DaemonSet
	found := &appsv1.DaemonSet{}
	getErr := r.Get(ctx, types.NamespacedName{Name: daemonSet.Name, Namespace: daemonSet.Namespace}, found)
	if getErr != nil && errors.IsNotFound(getErr) {
		logger.Info("Creating Fluentbit DaemonSet", "name", daemonSet.Name)
		return r.Create(ctx, daemonSet)
	} else if getErr != nil {
		return getErr
	}

	// Update if template spec changed - force update if the hash changed
	// This uses deep equality check for the pod spec to detect changes
	if !reflect.DeepEqual(found.Spec.Template.Spec, daemonSet.Spec.Template.Spec) ||
		found.Spec.Template.Annotations["kuberescue.io/config-hash"] != configHash {
		logger.Info("Updating Fluentbit DaemonSet", "name", daemonSet.Name,
			"reason", "config changed or spec updated")

		// Update the spec and annotations
		found.Spec = daemonSet.Spec
		found.Spec.Template.Annotations = daemonSet.Spec.Template.Annotations

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

	// Add this line:
	lr.Status.ObservedGeneration = lr.Generation

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
// Check for errors and Perform remediation if required
func (r *LogRemediationReconciler) checkLogsAndRemediate(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	logger := log.FromContext(ctx)

	// Skip if no remediation rules defined
	if len(lr.Spec.RemediationRules) == 0 {
		return nil
	}

	// Build app label filter from sources to narrow down the search
	var appLabels []string
	for _, source := range lr.Spec.Sources {
		if appLabel, ok := source.Selector["app"]; ok {
			appLabels = append(appLabels, appLabel)
		}
	}

	// Get Elasticsearch endpoint
	esEndpoint := fmt.Sprintf("http://%s:%d/%s/_search",
		lr.Spec.ElasticsearchConfig.Host,
		lr.Spec.ElasticsearchConfig.Port,
		lr.Spec.ElasticsearchConfig.Index)

	// Build query for error patterns
	var matchQueries []string
	for _, rule := range lr.Spec.RemediationRules {
		matchQueries = append(matchQueries, fmt.Sprintf(`{"match": {"log": "%s"}}`, rule.ErrorPattern))
	}

	// Build app label filter if we have any
	var appLabelFilter string
	if len(appLabels) > 0 {
		var appFilters []string
		for _, app := range appLabels {
			appFilters = append(appFilters, fmt.Sprintf(`{"match_phrase": {"kubernetes.labels.app": "%s"}}`, app))
		}
		appLabelFilter = fmt.Sprintf(`,"must": [{"bool": {"should": [%s]}}]`, strings.Join(appFilters, ","))
	}

	// This is a constrained time window to avoid getting stuck processing logs in the past
	// We Use either:
	// 1. The last processed timestamp if it's recent (within the max window)
	// 2. Or a limited time window in the past (e.g.,  minutes) to avoid falling too far behind

	// Max time window to look back for logs (3 minutes by default)
	maxLookbackWindow := 3 * time.Minute

	// Determine the time filter start point
	var timeFilterStart time.Time
	if lr.Status.LastProcessedTimestamp != nil {
		lastProcessed := lr.Status.LastProcessedTimestamp.Time
		// Use the last processed time if it's within our max window
		// Otherwise, use the max lookback window to avoid getting stuck in the past
		if time.Since(lastProcessed) <= maxLookbackWindow {
			timeFilterStart = lastProcessed
		} else {
			// If the last processed time is too far in the past
			// use a limited window of (e.g 3 Minutes) to avoid getting stuck processing old logs
			timeFilterStart = time.Now().Add(-maxLookbackWindow)
			logger.Info("Last processed timestamp is too old, using limited time window",
				"last_processed", lastProcessed,
				"using_window_from", timeFilterStart)
		}
	} else {
		// If no last processed timestamp, look back by the max window
		timeFilterStart = time.Now().Add(-maxLookbackWindow)
	}

	// Format the time for Elasticsearch
	timeFilter := timeFilterStart.Format(time.RFC3339)

	// Construct query with time constraints and ascending sort order
	query := fmt.Sprintf(`{
        "query": {
            "bool": {
                "should": [%s],
                "filter": [
                    {"range": {"@timestamp": {"gte": "%s"}}}
                ]%s
            }
        },
        "sort": [{"@timestamp": {"order": "asc"}}],
        "size": 100
    }`, strings.Join(matchQueries, ","), timeFilter, appLabelFilter)

	logger.Info("Querying Elasticsearch", "endpoint", esEndpoint, "timeFilter", timeFilter)

	// Query Elasticsearch
	req, err := http.NewRequest("POST", esEndpoint, strings.NewReader(query))
	if err != nil {
		metrics.LogProcessingErrors.Inc()
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	// Add authentication
	if lr.Spec.ElasticsearchConfig.SecretRef != "" {
		// In a real implementation, you would fetch credentials from the secret
		req.SetBasicAuth("elastic", "changeme")
	} else {
		req.SetBasicAuth("elastic", "changeme")
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		metrics.LogProcessingErrors.Inc()
		logger.Error(err, "Failed to query Elasticsearch")
		return err
	}
	defer resp.Body.Close()

	// Read response
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Check for successful response
	if resp.StatusCode != http.StatusOK {
		metrics.LogProcessingErrors.Inc()
		logger.Error(nil, "Elasticsearch query failed",
			"status", resp.Status,
			"body", string(body))
		return fmt.Errorf("elasticsearch query failed with status %s", resp.Status)
	}

	// Parse response
	var esResult map[string]interface{}
	if err := json.Unmarshal(body, &esResult); err != nil {
		metrics.LogProcessingErrors.Inc()
		logger.Error(err, "Failed to parse Elasticsearch response")
		return err
	}

	// Extract hits
	hits, ok := esResult["hits"]
	if !ok {
		metrics.LogProcessingErrors.Inc()
		logger.Error(nil, "Missing 'hits' field in response")
		return fmt.Errorf("missing 'hits' field in Elasticsearch response")
	}

	hitsMap, ok := hits.(map[string]interface{})
	if !ok {
		metrics.LogProcessingErrors.Inc()
		logger.Error(nil, "Expected 'hits' to be a map", "type", fmt.Sprintf("%T", hits))
		return fmt.Errorf("invalid 'hits' field type in Elasticsearch response")
	}

	// Check total hits count
	totalObj, hasTotalHits := hitsMap["total"]
	if hasTotalHits {
		totalMap, isMap := totalObj.(map[string]interface{})
		if isMap {
			if value, hasValue := totalMap["value"]; hasValue {
				logger.Info("Total matching documents", "count", value)
			}
		}
	}

	// Extract hits list
	hitsList, ok := hitsMap["hits"]
	if !ok {
		metrics.LogProcessingErrors.Inc()
		logger.Error(nil, "Missing 'hits.hits' field in response")
		return fmt.Errorf("missing 'hits.hits' field in Elasticsearch response")
	}

	hitsArray, ok := hitsList.([]interface{})
	if !ok {
		metrics.LogProcessingErrors.Inc()
		logger.Error(nil, "Expected 'hits.hits' to be an array", "type", fmt.Sprintf("%T", hitsList))
		return fmt.Errorf("invalid 'hits.hits' field type in Elasticsearch response")
	}

	logger.Info("Found log entries matching error patterns", "count", len(hitsArray))

	// If no hits, return early
	if len(hitsArray) == 0 {
		return nil
	}

	// Track processed timestamps to update lastProcessedTimestamp
	var latestTimestamp time.Time

	// Track active cooldown periods to avoid processing logs that would be ignored
	cooldownMap := make(map[string]time.Time)

	// Initialize cooldown map from existing remediation history
	for _, history := range lr.Status.RemediationHistory {
		for _, rule := range lr.Spec.RemediationRules {
			if rule.ErrorPattern == history.Pattern {
				key := fmt.Sprintf("%s:%s", history.Pattern, history.PodName)
				cooldownExpiry := history.Timestamp.Add(time.Duration(rule.CooldownPeriod) * time.Second)
				cooldownMap[key] = cooldownExpiry
				break
			}
		}
	}

	// Track number of actions performed
	actionsPerformed := 0

	// Process hits and perform remediation
	for _, hitObj := range hitsArray {
		hitMap, ok := hitObj.(map[string]interface{})
		if !ok {
			continue
		}

		source, ok := hitMap["_source"].(map[string]interface{})
		if !ok {
			continue
		}

		// Extract timestamp
		var logTimestamp time.Time
		if timestampRaw, hasTimestamp := source["@timestamp"]; hasTimestamp {
			if timestampStr, ok := timestampRaw.(string); ok {
				var parseErr error
				logTimestamp, parseErr = time.Parse(time.RFC3339, timestampStr)
				if parseErr != nil {
					// Try alternative format if RFC3339 fails
					logTimestamp, parseErr = time.Parse("2006-01-02T15:04:05.000Z", timestampStr)
					if parseErr != nil {
						// Default to current time if parsing fails
						logTimestamp = time.Now()
					}
				}
			}
		} else {
			// Default to current time if no timestamp
			logTimestamp = time.Now()
		}

		// Skip very old logs (older than our max window)
		if time.Since(logTimestamp) > maxLookbackWindow {
			logger.Info("Skipping old log entry outside our time window",
				"log_time", logTimestamp,
				"window_start", timeFilterStart)
			continue
		}

		// Update latest timestamp we've seen
		if logTimestamp.After(latestTimestamp) {
			latestTimestamp = logTimestamp
		}

		// Extract log message
		logMsg := ""
		logPaths := []string{"log", "message", "log_processed"}
		for _, path := range logPaths {
			if logRaw, hasLog := source[path]; hasLog {
				if logStr, ok := logRaw.(string); ok {
					logMsg = logStr
					break
				}
			}
		}

		// Check nested kubernetes log
		if logMsg == "" {
			if k8s, hasK8s := source["kubernetes"].(map[string]interface{}); hasK8s {
				if containerLog, hasLog := k8s["log"].(string); hasLog {
					logMsg = containerLog
				}
			}
		}

		if logMsg == "" {
			logger.Info("No log message found in document")
			continue
		}

		// Extract pod info (name and namespace)
		podName := ""
		namespace := ""

		// Try standard kubernetes format
		k8sRaw, hasK8s := source["kubernetes"]
		if hasK8s {
			if k8s, ok := k8sRaw.(map[string]interface{}); ok {
				// Try common fields for pod name
				podNameFields := []string{"pod_name", "pod", "pod_id", "container_name"}
				for _, field := range podNameFields {
					if val, has := k8s[field]; has {
						if str, ok := val.(string); ok {
							podName = str
							break
						}
					}
				}

				// Try common fields for namespace
				nsFields := []string{"namespace_name", "namespace", "ns"}
				for _, field := range nsFields {
					if val, has := k8s[field]; has {
						if str, ok := val.(string); ok {
							namespace = str
							break
						}
					}
				}

				// Check labels
				if labels, hasLabels := k8s["labels"].(map[string]interface{}); hasLabels {
					if val, has := labels["pod-template-hash"]; has && podName == "" {
						if str, ok := val.(string); ok {
							podName = str
						}
					}
					if val, has := labels["namespace"]; has && namespace == "" {
						if str, ok := val.(string); ok {
							namespace = str
						}
					}
				}
			}
		}

		// Fallbacks for pod/namespace
		if podName == "" || namespace == "" {
			// Check metadata
			if metadataRaw, has := source["metadata"]; has {
				if metadata, ok := metadataRaw.(map[string]interface{}); ok {
					if val, has := metadata["pod"]; has && podName == "" {
						if str, ok := val.(string); ok {
							podName = str
						}
					}
					if val, has := metadata["namespace"]; has && namespace == "" {
						if str, ok := val.(string); ok {
							namespace = str
						}
					}
				}
			}
		}

		// More fallbacks for pod name
		if podName == "" && k8sRaw != nil {
			if k8s, ok := k8sRaw.(map[string]interface{}); ok {
				if val, has := k8s["container_name"]; has {
					if str, ok := val.(string); ok {
						parts := strings.Split(str, "-")
						if len(parts) > 1 {
							podName = strings.Join(parts[:len(parts)-1], "-")
						} else {
							podName = str
						}
					}
				}
			}
		}

		// Default namespace
		if namespace == "" {
			namespace = lr.Namespace
		}

		// Last resort - extract pod from log message
		if podName == "" {
			podPattern := regexp.MustCompile(`pod[=:]\s*([a-zA-Z0-9-]+)`)
			if matches := podPattern.FindStringSubmatch(logMsg); len(matches) > 1 {
				podName = matches[1]
			}

			// Try app labels
			if podName == "" && len(appLabels) > 0 && namespace != "" {
				podList := &corev1.PodList{}
				if err := r.List(ctx, podList); err != nil {
					logger.Error(err, "Failed to list pods")
					continue
				}

				for _, pod := range podList.Items {
					if pod.Namespace == namespace {
						for _, appLabel := range appLabels {
							if pod.Labels["app"] == appLabel {
								podName = pod.Name
								break
							}
						}
					}
					if podName != "" {
						break
					}
				}
			}

			if podName == "" {
				logger.Info("Could not determine pod name, skipping", "log", logMsg)
				continue
			}
		}

		// Now check each remediation rule
		for _, rule := range lr.Spec.RemediationRules {
			matched, err := regexp.MatchString(rule.ErrorPattern, logMsg)
			if err != nil {
				logger.Error(err, "Error matching pattern", "pattern", rule.ErrorPattern)
				continue
			}

			if !matched {
				continue
			}

			logger.Info("Error pattern matched",
				"pattern", rule.ErrorPattern,
				"pod", podName,
				"namespace", namespace,
				"log_timestamp", logTimestamp)

			// Check cooldown
			cooldownKey := fmt.Sprintf("%s:%s", rule.ErrorPattern, podName)
			if cooldownExpiry, hasCooldown := cooldownMap[cooldownKey]; hasCooldown {
				if time.Now().Before(cooldownExpiry) {
					logger.Info("Skipping due to active cooldown period",
						"pod", podName,
						"pattern", rule.ErrorPattern,
						"cooldown_expires", cooldownExpiry)
					continue
				}
			}

			// Check if this log is too old to act on (within time window but older than 1 minute)
			if time.Since(logTimestamp) > time.Minute {
				logger.Info("Log is older than 1 minute but within window, checking if still relevant",
					"log_time", logTimestamp,
					"age", time.Since(logTimestamp))

				// For logs that are a bit old, we should verify if they're still relevant
				// based on the action type. For example:

				// For "scale" actions, check current scale status
				if rule.Action == "scale" || rule.Action == "recovery" {
					// Verify if scaling is still needed by checking the pod's owner's current state
					// This would involve a check against the current state of the deployment/statefulset

					// For demo purposes, we'll just log and proceed, but in production
					// you would implement actual state checking here
					logger.Info("Would verify scaling need here in production")
				}

				// For "restart" actions, check if pod is already restarted
				if rule.Action == "restart" {
					// Verify if the pod still exists and its start time
					pod := &corev1.Pod{}
					err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: namespace}, pod)

					// If pod not found or was started after the log timestamp, no need to restart
					if err != nil || (err == nil && pod.Status.StartTime != nil &&
						pod.Status.StartTime.After(logTimestamp)) {

						logger.Info("Pod already restarted or doesn't exist, skipping restart action",
							"pod", podName,
							"pod_exists", err == nil,
							"start_time", pod.Status.StartTime)
						continue
					}
				}
			}

			// Perform remediation
			var remediationErr error
			switch rule.Action {
			case "restart":
				remediationErr = r.performPodRestart(ctx, lr, namespace, podName, rule.ErrorPattern)
			case "scale":
				remediationErr = r.performResourceScaling(ctx, lr, namespace, podName, rule.ErrorPattern, false)
			case "recovery":
				remediationErr = r.performResourceScaling(ctx, lr, namespace, podName, rule.ErrorPattern, true)
			case "exec":
				logger.Info("Exec remediation not implemented yet")
				continue
			}

			if remediationErr != nil {
				logger.Error(remediationErr, "Failed to execute remediation action",
					"action", rule.Action,
					"pod", podName)
				continue
			}

			// Action succeeded - update cooldown
			cooldownExpiry := time.Now().Add(time.Duration(rule.CooldownPeriod) * time.Second)
			cooldownMap[cooldownKey] = cooldownExpiry
			actionsPerformed++

			// Refetch LogRemediation to get latest status
			if err := r.Get(ctx, types.NamespacedName{Name: lr.Name, Namespace: lr.Namespace}, lr); err != nil {
				logger.Error(err, "Failed to refetch LogRemediation")
				// Continue with stale data
			}

			// Only process first matching rule
			break
		}

		// Limit actions per reconciliation
		if actionsPerformed >= 3 {
			logger.Info("Reached maximum remediation actions", "count", actionsPerformed)
			break
		}
	}

	// Update LastProcessedTimestamp
	if !latestTimestamp.IsZero() {
		// Add a small buffer to avoid edge cases
		lr.Status.LastProcessedTimestamp = &metav1.Time{Time: latestTimestamp.Add(time.Second)}
		if err := r.Status().Update(ctx, lr); err != nil {
			logger.Error(err, "Failed to update last processed timestamp")
			return err
		}
	}

	logger.Info("Completed log remediation check", "actions_performed", actionsPerformed)
	return nil
}

// performPodRestart handles restarting a pod with proper owner detection
func (r *LogRemediationReconciler) performPodRestart(ctx context.Context, lr *remediationv1alpha1.LogRemediation,
	namespace, podName, errorPattern string) error {

	startTime := time.Now()
	defer func() {
		metrics.RemediationLatency.WithLabelValues("restart", namespace).Observe(time.Since(startTime).Seconds())
	}()

	logger := log.FromContext(ctx)
	logger.Info("Performing pod restart remediation", "pod", podName, "namespace", namespace)

	// Get the pod
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: namespace}, pod); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod no longer exists, skipping restart", "pod", podName)
			return nil
		}
		return fmt.Errorf("failed to get pod: %w", err)
	}

	// Delete the pod (it will be recreated by the controller)
	if err := r.Delete(ctx, pod); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod was already deleted", "pod", podName)
			return nil
		}
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	logger.Info("Pod deleted successfully, will be recreated by controller", "pod", podName)

	metrics.RemediationsTotal.WithLabelValues("restart", errorPattern, namespace).Inc()

	// Record the remediation action
	return r.recordRemediationAction(ctx, lr, podName, errorPattern, "restart")
}

// performResourceScaling handles scaling resources with automatic owner detection
// isScaleDown indicates whether this is a scale-down (recovery) operation
func (r *LogRemediationReconciler) performResourceScaling(ctx context.Context, lr *remediationv1alpha1.LogRemediation,
	namespace, podName, errorPattern string, isScaleDown bool) error {

	actionName := "scale"

	logger := log.FromContext(ctx)
	logger.Info("Performing scaling remediation", "pod", podName, "namespace", namespace, "isScaleDown", isScaleDown)

	startTime := time.Now()
	defer func() {
		metrics.RemediationLatency.WithLabelValues("scale", namespace).Observe(time.Since(startTime).Seconds())
	}()

	// Get the pod to determine its owner
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: namespace}, pod); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod no longer exists, skipping scaling", "pod", podName)
			return nil
		}
		return fmt.Errorf("failed to get pod: %w", err)
	}

	// Find the owner reference that's a scalable resource
	ownerKind := ""
	ownerName := ""

	for _, owner := range pod.OwnerReferences {
		if owner.Controller != nil && *owner.Controller {
			// Check if it's a kind we can scale
			switch owner.Kind {
			case "ReplicaSet":
				// For ReplicaSets, we need to find the Deployment that owns it
				rs := &appsv1.ReplicaSet{}
				if err := r.Get(ctx, types.NamespacedName{Name: owner.Name, Namespace: namespace}, rs); err != nil {
					logger.Error(err, "Failed to get ReplicaSet", "name", owner.Name)
					continue
				}

				// Find the deployment that owns this ReplicaSet
				for _, rsOwner := range rs.OwnerReferences {
					if rsOwner.Kind == "Deployment" && rsOwner.Controller != nil && *rsOwner.Controller {
						ownerKind = "Deployment"
						ownerName = rsOwner.Name
						break
					}
				}
			case "StatefulSet", "Deployment":
				ownerKind = owner.Kind
				ownerName = owner.Name
			}
		}

		if ownerKind != "" {
			break // Found a scalable owner
		}
	}

	if ownerKind == "" || ownerName == "" {
		return fmt.Errorf("could not find a scalable owner for pod %s", podName)
	}

	logger.Info("Found scalable owner", "kind", ownerKind, "name", ownerName)

	// Get current replica count based on owner kind
	var currentReplicas int32
	var maxReplicas int32 = 10 // Default max replicas, could be configurable via CRD
	var minReplicas int32 = 1  // Minimum number of replicas

	switch ownerKind {
	case "Deployment":
		deployment := &appsv1.Deployment{}
		if err := r.Get(ctx, types.NamespacedName{Name: ownerName, Namespace: namespace}, deployment); err != nil {
			return fmt.Errorf("failed to get deployment: %w", err)
		}

		if deployment.Spec.Replicas != nil {
			currentReplicas = *deployment.Spec.Replicas
		} else {
			currentReplicas = 1 // Default
		}

		// Check for HPA and respect its maxReplicas if it exists
		hpaList := &autoscalingv1.HorizontalPodAutoscalerList{}
		if err := r.List(ctx, hpaList, client.InNamespace(namespace)); err == nil {
			for _, hpa := range hpaList.Items {
				if hpa.Spec.ScaleTargetRef.Kind == "Deployment" && hpa.Spec.ScaleTargetRef.Name == ownerName {
					maxReplicas = hpa.Spec.MaxReplicas
					if hpa.Spec.MinReplicas != nil {
						minReplicas = *hpa.Spec.MinReplicas
					}
					logger.Info("Found HPA, using its min/max replicas",
						"hpa", hpa.Name,
						"minReplicas", minReplicas,
						"maxReplicas", maxReplicas)
					break
				}
			}
		}

	case "StatefulSet":
		statefulSet := &appsv1.StatefulSet{}
		if err := r.Get(ctx, types.NamespacedName{Name: ownerName, Namespace: namespace}, statefulSet); err != nil {
			return fmt.Errorf("failed to get statefulset: %w", err)
		}

		if statefulSet.Spec.Replicas != nil {
			currentReplicas = *statefulSet.Spec.Replicas
		} else {
			currentReplicas = 1 // Default
		}
	}

	var newReplicas int32

	if isScaleDown {
		actionName = "recovery"
		// Scale down logic - reduce by 25% of current replicas
		scaleIncrement := int32(math.Ceil(float64(currentReplicas) * 0.25))
		if scaleIncrement < 1 {
			scaleIncrement = 1
		}

		newReplicas = currentReplicas - scaleIncrement
		if newReplicas < minReplicas {
			newReplicas = minReplicas
		}

		// Don't do anything if already at minimum
		if currentReplicas <= minReplicas {
			logger.Info("Resource is already at minimum replicas, no scaling needed",
				"kind", ownerKind, "name", ownerName, "currentReplicas", currentReplicas, "minReplicas", minReplicas)

			// Record no-op action
			return r.recordRemediationAction(ctx, lr, podName, errorPattern, "recovery:no-op:min")
		}

		logger.Info("Recovery action: scaling down resource", "kind", ownerKind, "name", ownerName,
			"from", currentReplicas, "to", newReplicas)
	} else {
		// Scale up logic
		// If already at max replicas, switch to restart
		if currentReplicas >= maxReplicas {
			logger.Info("Resource is already at max replicas, switching to restart remediation",
				"kind", ownerKind, "name", ownerName, "currentReplicas", currentReplicas, "maxReplicas", maxReplicas)

			// When scaling is maxed out, fall back to restart
			return r.performPodRestart(ctx, lr, namespace, podName, errorPattern)
		}

		// Implement progressive scaling
		// Scale by 25% rounded up, with minimum of 1, to get to max replicas faster for critical issues
		scaleIncrement := int32(math.Ceil(float64(currentReplicas) * 0.25))
		if scaleIncrement < 1 {
			scaleIncrement = 1
		}

		newReplicas = currentReplicas + scaleIncrement
		if newReplicas > maxReplicas {
			newReplicas = maxReplicas
		}

		logger.Info("Scaling up resource", "kind", ownerKind, "name", ownerName,
			"from", currentReplicas, "to", newReplicas)
	}

	// Apply the scaling based on owner kind
	switch ownerKind {
	case "Deployment":
		metrics.RemediationsTotal.WithLabelValues(actionName, errorPattern, namespace).Inc()
		return r.scaleDeployment(ctx, namespace, ownerName, newReplicas, lr, podName, errorPattern)
	case "StatefulSet":
		metrics.RemediationsTotal.WithLabelValues(actionName, errorPattern, namespace).Inc()
		return r.scaleStatefulSet(ctx, namespace, ownerName, newReplicas, lr, podName, errorPattern)
	}

	return fmt.Errorf("unsupported owner kind: %s", ownerKind)
}

// scaleDeployment scales a deployment to the specified number of replicas
func (r *LogRemediationReconciler) scaleDeployment(ctx context.Context, namespace, name string,
	replicas int32, lr *remediationv1alpha1.LogRemediation, podName, errorPattern string) error {

	logger := log.FromContext(ctx)

	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		return err
	}

	// Update replicas
	deployment.Spec.Replicas = &replicas

	// Apply the update
	if err := r.Update(ctx, deployment); err != nil {
		return err
	}

	logger.Info("Deployment scaled successfully", "name", name, "replicas", replicas)

	// Record the action
	return r.recordRemediationAction(ctx, lr, podName, errorPattern, fmt.Sprintf("scale:%d", replicas))
}

// scaleStatefulSet scales a statefulset to the specified number of replicas
func (r *LogRemediationReconciler) scaleStatefulSet(ctx context.Context, namespace, name string,
	replicas int32, lr *remediationv1alpha1.LogRemediation, podName, errorPattern string) error {

	logger := log.FromContext(ctx)

	// Get the statefulset
	statefulSet := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulSet); err != nil {
		return err
	}

	// Update replicas
	statefulSet.Spec.Replicas = &replicas

	// Apply the update
	if err := r.Update(ctx, statefulSet); err != nil {
		return err
	}

	logger.Info("StatefulSet scaled successfully", "name", name, "replicas", replicas)

	// Record the action
	return r.recordRemediationAction(ctx, lr, podName, errorPattern, fmt.Sprintf("scale:%d", replicas))
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
