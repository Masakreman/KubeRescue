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
	"io"
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

// reconcile LogRemediation object
type LogRemediationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Standard rbac configuraion for kubebuilder, with additional rbac for apps and autoscaling

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

func (r *LogRemediationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling LogRemediation", "name", req.Name, "namespace", req.Namespace)

	//get LogRemediation Instance
	logRemediation := &remediationv1alpha1.LogRemediation{}
	err := r.Get(ctx, req.NamespacedName, logRemediation)
	if err != nil {
		if errors.IsNotFound(err) {
			metrics.ActiveRemediations.WithLabelValues(req.Namespace).Dec()
			logger.Info("LogRemediation resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get LogRemediation")
		return ctrl.Result{}, err
	}

	if logRemediation.Status.LastConfigured == nil {
		metrics.ActiveRemediations.WithLabelValues(req.Namespace).Inc()
	}

	//only reconcile if needed
	var skipResourceReconciliation bool

	if logRemediation.Status.LastConfigured != nil {
		lastReconciled := logRemediation.Status.LastConfigured.Time
		if time.Since(lastReconciled) < time.Minute*2 &&
			logRemediation.Generation == logRemediation.Status.ObservedGeneration {
			logger.Info("Skipping resource reconciliation, checking logs only")
			skipResourceReconciliation = true
		}
	}

	// Handle finalizers and deletion
	finalizerName := "kuberescue.io/finalizer"
	if logRemediation.ObjectMeta.DeletionTimestamp.IsZero() {
		//resource noot being deleted ensure so make sure it has finalisers
		if !containsString(logRemediation.ObjectMeta.Finalizers, finalizerName) {
			logRemediation.ObjectMeta.Finalizers = append(logRemediation.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(ctx, logRemediation); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		//delete resource
		if containsString(logRemediation.ObjectMeta.Finalizers, finalizerName) {
			//run finalisers
			if err := r.finaliseLogRemediation(ctx, logRemediation); err != nil {
				return ctrl.Result{}, err
			}

			//delete finaliser once finished
			logRemediation.ObjectMeta.Finalizers = removeString(logRemediation.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(ctx, logRemediation); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !skipResourceReconciliation {
		//create or update fluentbit configmap
		if err := r.reconcileFluentbitConfigMap(ctx, logRemediation); err != nil {
			logger.Error(err, "Failed to reconcile Fluentbit ConfigMap")
			r.updateLogRemediationStatus(ctx, logRemediation, "ConfigMapFailed", "Failed to create or update Fluentbit ConfigMap", metav1.ConditionFalse)
			return ctrl.Result{RequeueAfter: time.Second * 10}, err
		}

		//create or update ds for fluentbit
		if err := r.reconcileFluentbitDaemonSet(ctx, logRemediation); err != nil {
			logger.Error(err, "Failed to reconcile Fluentbit DaemonSet")
			r.updateLogRemediationStatus(ctx, logRemediation, "DaemonSetFailed", "Failed to create or update Fluentbit DaemonSet", metav1.ConditionFalse)
			return ctrl.Result{RequeueAfter: time.Second * 10}, err
		}
		if err := r.updatePodStatus(ctx, logRemediation); err != nil {
			logger.Error(err, "Failed to update pod status")
			return ctrl.Result{RequeueAfter: time.Second * 10}, err
		}
		r.updateLogRemediationStatus(ctx, logRemediation, "Reconciled", "Successfully reconciled LogRemediation", metav1.ConditionTrue)
	}

	if len(logRemediation.Spec.RemediationRules) > 0 {
		logger.Info("Checking logs for remediation", "rules_count", len(logRemediation.Spec.RemediationRules))
		if err := r.parseLogsAndRemediate(ctx, logRemediation); err != nil {
			logger.Error(err, "Failed to check logs and remediate")
		}
	}

	return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
}

// cleanup after deletion
func (r *LogRemediationReconciler) finaliseLogRemediation(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	logger := log.FromContext(ctx)
	logger.Info("Finalising LogRemediation", "name", lr.Name, "namespace", lr.Namespace)
	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-fluentbit", lr.Name),
			Namespace: lr.Namespace,
		},
	}
	if err := r.Delete(ctx, daemonSet); err != nil && !errors.IsNotFound(err) {
		return err
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-fluentbit-config", lr.Name),
			Namespace: lr.Namespace,
		},
	}
	if err := r.Delete(ctx, configMap); err != nil && !errors.IsNotFound(err) {
		return err
	}

	logger.Info("Successfully finalised LogRemediation")
	return nil
}

func (r *LogRemediationReconciler) generateFluentbitConfig(lr *remediationv1alpha1.LogRemediation) string {
	instanceID := lr.Name
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
	if lr.Spec.FluentbitConfig != nil {
		if lr.Spec.FluentbitConfig.BufferSize != "" {
			config = strings.Replace(config, "Buffer_Size  5MB", fmt.Sprintf("Buffer_Size  %s", lr.Spec.FluentbitConfig.BufferSize), 1)
		}
		if lr.Spec.FluentbitConfig.FlushInterval != 0 {
			config = strings.Replace(config, "Flush        1", fmt.Sprintf("Flush        %d", lr.Spec.FluentbitConfig.FlushInterval), 1)
		}
	}

	pathPattern := "/var/log/containers/test*_*_*.log"

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
    Ignore_Older    5m 
    Exit_On_Eof     false

`, pathPattern, instanceID)

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

` // send everyting to elasticsearch
	config += `[OUTPUT]
    Name            stdout
    Match           kube.*
    Format          json_lines

`
	esConfig := lr.Spec.ElasticsearchConfig
	hostName := esConfig.Host
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

func (r *LogRemediationReconciler) reconcileFluentbitConfigMap(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	logger := log.FromContext(ctx)
	fbConfig := r.generateFluentbitConfig(lr)

	//definging parsers for Fluentbit
	parsersConfig := `[PARSER]
    Name   docker
    Format json
    Time_Key time
    Time_Format %Y-%m-%dT%H:%M:%S.%L
    Time_Keep On
`

	//init configmap
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

	if err := controllerutil.SetControllerReference(lr, configMap, r.Scheme); err != nil {
		return err
	}

	//create or update configmap
	found := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating Fluentbit ConfigMap", "name", configMap.Name)
		return r.Create(ctx, configMap)
	} else if err != nil {
		return err
	}
	//if config changed then update
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
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      fmt.Sprintf("%s-fluentbit-config", lr.Name),
		Namespace: lr.Namespace,
	}, configMap)

	configHash := fmt.Sprintf("%s-default", lr.Name)

	if err == nil {
		configContent := configMap.Data["fluent-bit.conf"]
		configHash = fmt.Sprintf("%s-%d", lr.Name, len(configContent))
	}

	//add labels for fluentbit resources
	labels := map[string]string{
		"app":        fmt.Sprintf("%s-fluentbit", lr.Name),
		"controller": lr.Name,
	}

	//create or update Fluentbit DaemonSet
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
						"kuberescue.io/config-hash": configHash,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "fluentbit",
					Containers: []corev1.Container{
						{
							Name:  "fluentbit",
							Image: "fluent/fluent-bit:2.1.10",
							Env: []corev1.EnvVar{
								{
									Name:  "ES_USER",
									Value: "elastic",
								},
								{
									Name:  "ES_PASSWORD",
									Value: "changeme",
								},
								{
									Name:  "FLUENT_INSTANCE",
									Value: lr.Name,
								},
								{
									Name:  "FLUENT_DB_RESET",
									Value: configHash,
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewMilliQuantity(200, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(300*1024*1024, resource.BinarySI),
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
	if lr.Spec.ElasticsearchConfig.SecretRef != "" {
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
	if err := controllerutil.SetControllerReference(lr, daemonSet, r.Scheme); err != nil {
		return err
	}

	//create or update DaemonSet
	found := &appsv1.DaemonSet{}
	getErr := r.Get(ctx, types.NamespacedName{Name: daemonSet.Name, Namespace: daemonSet.Namespace}, found)
	if getErr != nil && errors.IsNotFound(getErr) {
		logger.Info("Creating Fluentbit DaemonSet", "name", daemonSet.Name)
		return r.Create(ctx, daemonSet)
	} else if getErr != nil {
		return getErr
	}

	// check for changes and update
	if !reflect.DeepEqual(found.Spec.Template.Spec, daemonSet.Spec.Template.Spec) ||
		found.Spec.Template.Annotations["kuberescue.io/config-hash"] != configHash {
		logger.Info("Updating Fluentbit DaemonSet", "name", daemonSet.Name,
			"reason", "config changed or spec updated")

		found.Spec = daemonSet.Spec
		found.Spec.Template.Annotations = daemonSet.Spec.Template.Annotations

		return r.Update(ctx, found)
	}

	return nil
}
func (r *LogRemediationReconciler) updatePodStatus(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(lr.Namespace),
		client.MatchingLabels(map[string]string{"app": fmt.Sprintf("%s-fluentbit", lr.Name)}),
	}

	if err := r.List(ctx, podList, listOpts...); err != nil {
		return err
	}

	//extract pod names and update status
	var podNames []string
	for _, pod := range podList.Items {
		podNames = append(podNames, pod.Name)
	}
	lr.Status.FluentbitPods = podNames
	lr.Status.LastConfigured = &metav1.Time{Time: time.Now()}

	return r.Status().Update(ctx, lr)
}

func (r *LogRemediationReconciler) updateLogRemediationStatus(ctx context.Context, lr *remediationv1alpha1.LogRemediation, reason, message string, status metav1.ConditionStatus) error {
	meta.SetStatusCondition(&lr.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		ObservedGeneration: lr.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	})
	lr.Status.ObservedGeneration = lr.Generation

	return r.Status().Update(ctx, lr)
}
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

// setup controller
func (r *LogRemediationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&remediationv1alpha1.LogRemediation{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}

func (r *LogRemediationReconciler) parseLogsAndRemediate(ctx context.Context, lr *remediationv1alpha1.LogRemediation) error {
	logger := log.FromContext(ctx)

	// time the log processing
	startTime := time.Now()
	logsProcessed := 0
	cooldownCount := make(map[string]int)

	// ignore if no rules present
	if len(lr.Spec.RemediationRules) == 0 {
		return nil
	}

	// build an applabel to narrow searching
	var appLabels []string
	for _, source := range lr.Spec.Sources {
		if appLabel, ok := source.Selector["app"]; ok {
			appLabels = append(appLabels, appLabel)
		}
	}

	// elasticsearch url endpoint
	esEndpoint := fmt.Sprintf("http://%s:%d/%s/_search",
		lr.Spec.ElasticsearchConfig.Host,
		lr.Spec.ElasticsearchConfig.Port,
		lr.Spec.ElasticsearchConfig.Index)

	// make a query
	var matchQueries []string
	for _, rule := range lr.Spec.RemediationRules {
		matchQueries = append(matchQueries, fmt.Sprintf(`{"match": {"log": "%s"}}`, rule.ErrorPattern))
	}

	// building an applabel
	var appLabelFilter string
	if len(appLabels) > 0 {
		var appFilters []string
		for _, app := range appLabels {
			appFilters = append(appFilters, fmt.Sprintf(`{"match_phrase": {"kubernetes.labels.app": "%s"}}`, app))
		}
		appLabelFilter = fmt.Sprintf(`,"must": [{"bool": {"should": [%s]}}]`, strings.Join(appFilters, ","))
	}

	//max lookback time
	maxLookbackWindow := 3 * time.Minute

	// get time filter start point
	var timeFilterStart time.Time
	if lr.Status.LastProcessedTimestamp != nil {
		lastProcessed := lr.Status.LastProcessedTimestamp.Time
		if time.Since(lastProcessed) <= maxLookbackWindow {
			timeFilterStart = lastProcessed
		} else {
			// limited time window to not get stiuck processing logs in past
			timeFilterStart = time.Now().Add(-maxLookbackWindow)
			logger.Info("Last processed timestamp is too old, using limited time window",
				"last_processed", lastProcessed,
				"using_window_from", timeFilterStart)
		}
	} else {
		timeFilterStart = time.Now().Add(-maxLookbackWindow)
	}

	// elasticsearch documented way for filtering time
	timeFilter := timeFilterStart.Format(time.RFC3339)
	//qury logs in ascending order
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

	req, err := http.NewRequest("POST", esEndpoint, strings.NewReader(query))
	if err != nil {
		metrics.LogProcessingErrors.Inc()
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	if lr.Spec.ElasticsearchConfig.SecretRef != "" {
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		metrics.LogProcessingErrors.Inc()
		return err
	}

	if resp.StatusCode != http.StatusOK {
		metrics.LogProcessingErrors.Inc()
		logger.Error(nil, "Elasticsearch query failed",
			"status", resp.Status,
			"body", string(body))
		return fmt.Errorf("elasticsearch query failed with status %s", resp.Status)
	}
	var esResult map[string]interface{}
	if err := json.Unmarshal(body, &esResult); err != nil {
		metrics.LogProcessingErrors.Inc()
		logger.Error(err, "Failed to parse Elasticsearch response")
		return err
	}

	// extracing hits
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
	totalObj, hasTotalHits := hitsMap["total"]
	if hasTotalHits {
		totalMap, isMap := totalObj.(map[string]interface{})
		if isMap {
			if value, hasValue := totalMap["value"]; hasValue {
				logger.Info("Total matching documents", "count", value)
			}
		}
	}
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

	logger.Info("Found log entries matching error-patterns", "count", len(hitsArray))
	if len(hitsArray) == 0 {
		return nil
	}
	var latestTimestamp time.Time

	cooldownMap := make(map[string]time.Time)

	for _, history := range lr.Status.RemediationHistory {
		for _, rule := range lr.Spec.RemediationRules {
			if rule.ErrorPattern == history.Pattern {
				key := fmt.Sprintf("%s:%s", history.Pattern, history.PodName)
				cooldownExpiry := history.Timestamp.Add(time.Duration(rule.CooldownPeriod) * time.Second)
				cooldownMap[key] = cooldownExpiry
				actionKey := fmt.Sprintf("%s:%s", rule.Action, lr.Namespace)
				cooldownCount[actionKey]++
			}
		}
	}
	for actionNs, count := range cooldownCount {
		parts := strings.Split(actionNs, ":")
		if len(parts) == 2 {
			metrics.RemediationsInCooldown.WithLabelValues(parts[0], parts[1]).Set(float64(count))
		}
	}
	actionsPerformed := 0

	for _, hitObj := range hitsArray {
		logsProcessed++

		hitMap, ok := hitObj.(map[string]interface{})
		if !ok {
			continue
		}

		source, ok := hitMap["_source"].(map[string]interface{})
		if !ok {
			continue
		}
		var logTimestamp time.Time
		if timestampRaw, hasTimestamp := source["@timestamp"]; hasTimestamp {
			if timestampStr, ok := timestampRaw.(string); ok {
				var parseErr error
				logTimestamp, parseErr = time.Parse(time.RFC3339, timestampStr)
				if parseErr != nil {
					logTimestamp, parseErr = time.Parse("2006-01-02T15:04:05.000Z", timestampStr)
					if parseErr != nil {
						logTimestamp = time.Now()
					}
				}
			}
		} else {
			logTimestamp = time.Now()
		}
		if time.Since(logTimestamp) > maxLookbackWindow {
			logger.Info("Skipping old log entry outside our time window",
				"log_time", logTimestamp,
				"window_start", timeFilterStart)
			continue
		}

		if logTimestamp.After(latestTimestamp) {
			latestTimestamp = logTimestamp
		}

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
		//try extractiing metadata using common field names
		podName := ""
		namespace := ""
		var appLabel string

		k8sRaw, hasK8s := source["kubernetes"]
		if hasK8s {
			if k8s, ok := k8sRaw.(map[string]interface{}); ok {
				podNameFields := []string{"pod_name", "pod", "pod_id", "container_name"}
				for _, field := range podNameFields {
					if val, has := k8s[field]; has {
						if str, ok := val.(string); ok {
							podName = str
							break
						}
					}
				}
				nsFields := []string{"namespace_name", "namespace", "ns"}
				for _, field := range nsFields {
					if val, has := k8s[field]; has {
						if str, ok := val.(string); ok {
							namespace = str
							break
						}
					}
				}

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
					if val, has := labels["app"]; has {
						if str, ok := val.(string); ok {
							appLabel = str
						}
					}
				}
			}
		}
		if namespace == "" {
			namespace = lr.Namespace
		}
		if appLabel == "" {
			appLabel = "novalueprovided"
		}
		if podName == "" {
			podPattern := regexp.MustCompile(`pod[=:]\s*([a-zA-Z0-9-]+)`)
			if matches := podPattern.FindStringSubmatch(logMsg); len(matches) > 1 {
				podName = matches[1]
			}
			if podName == "" && len(appLabels) > 0 && namespace != "" {
				podList := &corev1.PodList{}
				if err := r.List(ctx, podList); err != nil {
					logger.Error(err, "Failed to list pods")
					continue
				}

				for _, pod := range podList.Items {
					if pod.Namespace == namespace {
						for _, label := range appLabels {
							if pod.Labels["app"] == label {
								podName = pod.Name
								appLabel = label
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
		for _, rule := range lr.Spec.RemediationRules {
			matched, err := regexp.MatchString(rule.ErrorPattern, logMsg)
			if err != nil {
				logger.Error(err, "Error matching pattern", "pattern", rule.ErrorPattern)
				continue
			}

			if !matched {
				continue
			}

			metrics.ErrorPatternOccurrences.WithLabelValues(rule.ErrorPattern, namespace, appLabel).Inc()

			logger.Info("Error pattern matched",
				"pattern", rule.ErrorPattern,
				"pod", podName,
				"namespace", namespace,
				"log_timestamp", logTimestamp)

			//checking cooldown
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
			//verify if log is too old
			if time.Since(logTimestamp) > time.Minute {
				logger.Info("Log is older than 1 minute but within window, checking if still relevant",
					"log_time", logTimestamp,
					"age", time.Since(logTimestamp))

				if rule.Action == "scale" || rule.Action == "recovery" {
					logger.Info("continue")
				}

				// check if already restarted
				if rule.Action == "restart" {
					pod := &corev1.Pod{}
					err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: namespace}, pod)

					if err != nil || (err == nil && pod.Status.StartTime != nil &&
						pod.Status.StartTime.After(logTimestamp)) {

						logger.Info("Pod already restarted or doesn't exist, ignoreing restart action",
							"pod", podName,
							"pod_exists", err == nil,
							"start_time", pod.Status.StartTime)
						continue
					}
				}
			}
			var remediationErr error
			switch rule.Action {
			case "restart":
				remediationErr = r.performPodRestart(ctx, lr, namespace, podName, rule.ErrorPattern)
			case "scale":
				remediationErr = r.doResourceScaling(ctx, lr, namespace, podName, rule.ErrorPattern, false)
			case "recovery":
				remediationErr = r.doResourceScaling(ctx, lr, namespace, podName, rule.ErrorPattern, true)
			case "exec":
				logger.Info("TODO: implement this")
				continue
			}

			if remediationErr != nil {
				logger.Error(remediationErr, "Failed to execute remediation action",
					"action", rule.Action,
					"pod", podName)
				metrics.RemediationFailureTotal.WithLabelValues(rule.Action, namespace, remediationErr.Error()).Inc()
				continue
			}

			metrics.RemediationSuccessTotal.WithLabelValues(rule.Action, namespace).Inc()

			cooldownExpiry := time.Now().Add(time.Duration(rule.CooldownPeriod) * time.Second)
			cooldownMap[cooldownKey] = cooldownExpiry

			actionKey := fmt.Sprintf("%s:%s", rule.Action, namespace)
			cooldownCount[actionKey]++
			metrics.RemediationsInCooldown.WithLabelValues(rule.Action, namespace).Set(float64(cooldownCount[actionKey]))

			actionsPerformed++

			if err := r.Get(ctx, types.NamespacedName{Name: lr.Name, Namespace: lr.Namespace}, lr); err != nil {
				logger.Error(err, "Failed to refetch LogRemediation")
			}
			break
		}

		if actionsPerformed >= 3 {
			logger.Info("Reached maximum remediation actions", "count", actionsPerformed)
			break
		}
	}

	if !latestTimestamp.IsZero() {
		lr.Status.LastProcessedTimestamp = &metav1.Time{Time: latestTimestamp.Add(time.Second)}
		if err := r.Status().Update(ctx, lr); err != nil {
			logger.Error(err, "Failed to update last processed timestamp")
			return err
		}
	}
	metrics.LogsProcessedTotal.Add(float64(logsProcessed))
	metrics.LogProcessingDuration.WithLabelValues(lr.Namespace).Observe(time.Since(startTime).Seconds())

	logger.Info("Completed log remediation check", "actions_performed", actionsPerformed, "logs_processed", logsProcessed)
	return nil
}

func (r *LogRemediationReconciler) performPodRestart(ctx context.Context, lr *remediationv1alpha1.LogRemediation,
	namespace, podName, errorPattern string) error {

	startTime := time.Now()
	defer func() {
		metrics.RemediationLatency.WithLabelValues("restart", namespace).Observe(time.Since(startTime).Seconds())
	}()

	logger := log.FromContext(ctx)
	logger.Info("Performing pod restart remediation", "pod", podName, "namespace", namespace)
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: namespace}, pod); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod no longer exists, skipping restart", "pod", podName)
			return nil
		}
		return fmt.Errorf("failed to get pod: %w", err)
	}
	if err := r.Delete(ctx, pod); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod was already deleted", "pod", podName)
			return nil
		}
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	logger.Info("Pod deleted successfully, will be recreated by controller", "pod", podName)
	metrics.RemediationsTotal.WithLabelValues("restart", errorPattern, namespace).Inc()

	return r.recordRemediationAction(ctx, lr, podName, errorPattern, "restart")
}

// handle scaling
func (r *LogRemediationReconciler) doResourceScaling(ctx context.Context, lr *remediationv1alpha1.LogRemediation,
	namespace, podName, errorPattern string, isScaleDown bool) error {

	actionName := "scale"

	logger := log.FromContext(ctx)
	logger.Info("Performing scaling remediation", "pod", podName, "namespace", namespace, "isScaleDown", isScaleDown)

	startTime := time.Now()
	defer func() {
		metrics.RemediationLatency.WithLabelValues("scale", namespace).Observe(time.Since(startTime).Seconds())
	}()
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: namespace}, pod); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Pod no longer exists, skipping scaling", "pod", podName)
			return nil
		}
		return fmt.Errorf("failed to get pod: %w", err)
	}

	resourceKind := ""
	resourceName := ""

	for _, owner := range pod.OwnerReferences {
		if owner.Controller != nil && *owner.Controller {
			switch owner.Kind {
			case "ReplicaSet":
				rs := &appsv1.ReplicaSet{}
				if err := r.Get(ctx, types.NamespacedName{Name: owner.Name, Namespace: namespace}, rs); err != nil {
					logger.Error(err, "Failed to get ReplicaSet", "name", owner.Name)
					continue
				}
				for _, rsOwner := range rs.OwnerReferences {
					if rsOwner.Kind == "Deployment" && rsOwner.Controller != nil && *rsOwner.Controller {
						resourceKind = "Deployment"
						resourceName = rsOwner.Name
						break
					}
				}
			case "StatefulSet", "Deployment":
				resourceKind = owner.Kind
				resourceName = owner.Name
			}
		}

		if resourceKind != "" {
			break
		}
	}

	if resourceKind == "" || resourceName == "" {
		return fmt.Errorf("could not find a scalable owner for pod %s", podName)
	}

	logger.Info("Found scalable owner", "kind", resourceKind, "name", resourceName)

	var currentReplicas int32
	var maxReplicas int32 = 10
	var minReplicas int32 = 1

	switch resourceKind {
	case "Deployment":
		deployment := &appsv1.Deployment{}
		if err := r.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace}, deployment); err != nil {
			return fmt.Errorf("failed to get deployment: %w", err)
		}

		if deployment.Spec.Replicas != nil {
			currentReplicas = *deployment.Spec.Replicas
		} else {
			currentReplicas = 1
		}

		//check if HPA is used
		hpaList := &autoscalingv1.HorizontalPodAutoscalerList{}
		if err := r.List(ctx, hpaList, client.InNamespace(namespace)); err == nil {
			for _, hpa := range hpaList.Items {
				if hpa.Spec.ScaleTargetRef.Kind == "Deployment" && hpa.Spec.ScaleTargetRef.Name == resourceName {
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
		if err := r.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace}, statefulSet); err != nil {
			return fmt.Errorf("failed to get statefulset: %w", err)
		}

		if statefulSet.Spec.Replicas != nil {
			currentReplicas = *statefulSet.Spec.Replicas
		} else {
			currentReplicas = 1
		}
	}

	var newReplicas int32

	if isScaleDown {
		actionName = "recovery"
		scaleIncrement := int32(math.Ceil(float64(currentReplicas) * 0.25))
		if scaleIncrement < 1 {
			scaleIncrement = 1
		}
		newReplicas = currentReplicas - scaleIncrement
		if newReplicas < minReplicas {
			newReplicas = minReplicas
		}
		if currentReplicas <= minReplicas {
			logger.Info("Resource is already at minimum replicas, no scaling needed",
				"kind", resourceKind, "name", resourceName, "currentReplicas", currentReplicas, "minReplicas", minReplicas)

			return r.recordRemediationAction(ctx, lr, podName, errorPattern, "recovery:no-op:min")
		}

		logger.Info("Recovery action: scaling down resource", "kind", resourceKind, "name", resourceName,
			"from", currentReplicas, "to", newReplicas)
	} else {
		if currentReplicas >= maxReplicas {
			logger.Info("Resource is already at max replicas, switching to restart remediation",
				"kind", resourceKind, "name", resourceName, "currentReplicas", currentReplicas, "maxReplicas", maxReplicas)

			//restart fallback
			return r.performPodRestart(ctx, lr, namespace, podName, errorPattern)
		}

		scaleIncrement := int32(math.Ceil(float64(currentReplicas) * 0.25))
		if scaleIncrement < 1 {
			scaleIncrement = 1
		}

		newReplicas = currentReplicas + scaleIncrement
		if newReplicas > maxReplicas {
			newReplicas = maxReplicas
		}

		logger.Info("Scaling up resource", "kind", resourceKind, "name", resourceName,
			"from", currentReplicas, "to", newReplicas)
	}

	switch resourceKind {
	case "Deployment":
		metrics.RemediationsTotal.WithLabelValues(actionName, errorPattern, namespace).Inc()
		return r.scaleDeployment(ctx, namespace, resourceName, newReplicas, lr, podName, errorPattern)
	case "StatefulSet":
		metrics.RemediationsTotal.WithLabelValues(actionName, errorPattern, namespace).Inc()
		return r.scaleStatefulSet(ctx, namespace, resourceName, newReplicas, lr, podName, errorPattern)
	}

	return fmt.Errorf("unsupported owner kind: %s", resourceKind)
}

// scale deployment
func (r *LogRemediationReconciler) scaleDeployment(ctx context.Context, namespace, name string,
	replicas int32, lr *remediationv1alpha1.LogRemediation, podName, errorPattern string) error {

	logger := log.FromContext(ctx)

	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		return err
	}
	var currentReplicas int32 = 1
	if deployment.Spec.Replicas != nil {
		currentReplicas = *deployment.Spec.Replicas
	}

	direction := "up"
	if replicas < currentReplicas {
		direction = "down"
	}

	deployment.Spec.Replicas = &replicas
	if err := r.Update(ctx, deployment); err != nil {
		return err
	}

	logger.Info("Deployment scaled successfully", "name", name, "replicas", replicas)

	metrics.ResourceScalingOperations.WithLabelValues("Deployment", name, namespace, direction).Inc()
	metrics.ResourceCurrentReplicas.WithLabelValues("Deployment", name, namespace).Set(float64(replicas))

	return r.recordRemediationAction(ctx, lr, podName, errorPattern, fmt.Sprintf("scale:%d", replicas))
}

// scalestateful set up or down
func (r *LogRemediationReconciler) scaleStatefulSet(ctx context.Context, namespace, name string,
	replicas int32, lr *remediationv1alpha1.LogRemediation, podName, errorPattern string) error {

	logger := log.FromContext(ctx)

	statefulSet := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulSet); err != nil {
		return err
	}

	var currentReplicas int32 = 1
	if statefulSet.Spec.Replicas != nil {
		currentReplicas = *statefulSet.Spec.Replicas
	}

	direction := "up"
	if replicas < currentReplicas {
		direction = "down"
	}

	statefulSet.Spec.Replicas = &replicas
	if err := r.Update(ctx, statefulSet); err != nil {
		return err
	}

	logger.Info("StatefulSet successfully scaled", "name", name, "replicas", replicas)

	metrics.ResourceScalingOperations.WithLabelValues("StatefulSet", name, namespace, direction).Inc()
	metrics.ResourceCurrentReplicas.WithLabelValues("StatefulSet", name, namespace).Set(float64(replicas))

	return r.recordRemediationAction(ctx, lr, podName, errorPattern, fmt.Sprintf("scale:%d", replicas))
}

// record remediation actions in cr status
func (r *LogRemediationReconciler) recordRemediationAction(ctx context.Context, lr *remediationv1alpha1.LogRemediation, podName, pattern, action string) error {

	addStatus := remediationv1alpha1.RemediationHistoryEntry{
		Timestamp: metav1.Now(),
		PodName:   podName,
		Pattern:   pattern,
		Action:    action,
	}

	lr.Status.RemediationHistory = append(lr.Status.RemediationHistory, addStatus)

	return r.Status().Update(ctx, lr)
}
