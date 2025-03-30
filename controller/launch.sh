#! /bin/sh

# Prepare test Apps on the cluster
kubectl apply -f ./internal/controller/testApps/test1-db-error-app.yaml &
kubectl apply -f ./internal/controller/testApps/test2-memory-leak-app.yaml &
kubectl apply -f ./internal/controller/testApps/test3-service-cascade-app.yaml &
kubectl apply -f ./internal/controller/testApps/test4-traffic-spike-app.yaml &

wait

# Prepare Custom Resources to track the apps
kubectl apply -f ./internal/controller/testCRs/test1-db-error-remediation.yaml &
kubectl apply -f ./internal/controller/testCRs/test2-memory-leak-remediation.yaml &
kubectl apply -f ./internal/controller/testCRs/test3-service-cascade-remediation.yaml &
kubectl apply -f ./internal/controller/testCRs/test4-traffic-spike-remediation.yaml &

wait



