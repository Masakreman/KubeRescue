#!/bin/bash
set -e

echo "Setting up AlertManager with PagerDuty integration..."

# Apply AlertManager config
kubectl apply -f config/prometheus/alerts/alertmanager-config.yaml

# Wait a bit for the secret to be created
sleep 2

# Apply AlertManager CR
kubectl apply -f config/prometheus/alerts/alertmanager-cr.yaml

# Wait for AlertManager to start
echo "Waiting for AlertManager to start..."
kubectl wait --for=condition=Available --timeout=300s alertmanager/main -n monitoring || true

# Apply test alert and KubeRescue alerts
kubectl apply -f config/prometheus/alerts/test-alert.yaml
kubectl apply -f config/prometheus/alerts/kuberescue-alerts.yaml

echo "Done! Check AlertManager at http://localhost:9093 (after port forwarding)"
echo "Test alert should appear within 30 seconds"