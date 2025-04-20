# KubeRescue

KubeRescue is an automated remediation system for Kubernetes applications that detects and fixes common failures by monitoring application logs.

## Overview

KubeRescue watches your application logs for error patterns and automatically performs remediation actions like restarting pods or scaling deployments. It helps reduce downtime and manual intervention for common failure scenarios.

Key features:
- Log pattern-based error detection
- Automatic remediation actions (restart, scale up/down)
- Cooldown periods to prevent remediation loops
- Prometheus metrics for monitoring remediation actions
- Grafana dashboards for visualizing error patterns and remediation history

## How It Works

1. KubeRescue deploys Fluent Bit as a DaemonSet to collect container logs
2. Logs are sent to Elasticsearch for storage and querying
3. The controller periodically checks for error patterns in logs
4. When a pattern matches, it triggers the configured remediation action
5. Actions and their results are recorded in the controller's status

## Getting Started

### Prerequisites
- Kubernetes cluster (v1.23+)
- kubectl configured to access your cluster
- Elasticsearch instance for log storage

### Installation

```sh
# Install the CRDs
make install

# Deploy the controller
make deploy IMG=masakreman/kuberescue-controller:latest

# Monitor the controller logs
kubectl logs -f -n controller-system controller-controller-manager-xxxxx
```

### Basic Usage

Create a LogRemediation resource that defines:
- Which applications to monitor (via label selectors)
- Error patterns to match in logs
- Remediation actions for each pattern

Example:

```yaml
apiVersion: remediation.kuberescue.io/v1alpha1
kind: LogRemediation
metadata:
  name: db-error-remediation
spec:
  sources:
  - type: deployment
    selector:
      app: my-app
  elasticsearchConfig:
    host: elasticsearch.default.svc
    port: 9200
    index: kubernetes-logs
  remediationRules:
  - errorPattern: "CRITICAL_DB_CONNECTION_FAILED"
    action: restart
    cooldownPeriod: 60  
  - errorPattern: "HIGH_MEMORY_USAGE"
    action: scale
    cooldownPeriod: 120 
```

## Available Remediation Actions

- **restart**: Restarts the pod where the error was detected
- **scale**: Increases the replica count of the deployment/statefulset
- **recovery**: Decreases the replica count (used after a scaling event)

## Monitoring

KubeRescue exposes Prometheus metrics for monitoring its operations:
- Error pattern occurrences
- Remediation actions taken
- Success/failure rates
- Current resource states

### Grafana Dashboards

The project includes pre-built Grafana dashboards:
- KubeRescue Overview
- Error Pattern Analysis
- Application Performance

## Configuration Tips

1. Start with longer cooldown periods to avoid excessive remediation
2. Use specific error patterns to avoid false positives
3. Test patterns on non-critical applications first
4. For scaling actions, consider setting up HPA thresholds as well

## Troubleshooting

Common issues:
- **No logs in Elasticsearch**: Check Fluent Bit pods are running and the connection to Elasticsearch is working
- **No remediation actions**: Verify error patterns match your application logs
- **Too many remediation actions**: Increase the cooldown period

## License

Copyright 2025. Licensed under the Apache License, Version 2.0.