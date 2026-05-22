#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-gpu-topo-test}"
IMAGE_NAME="gpu-topology-scheduler:latest"

echo "=== Creating kind cluster ==="
kind create cluster --name "$CLUSTER_NAME" --config hack/kind-config.yaml

echo "=== Labeling nodes ==="
# Simulate topology: worker1 & worker2 in same NVLink domain, worker3 in different rack
kubectl label node "${CLUSTER_NAME}-worker"  gpu-topology/nvlink-domain=rack01-domain01 gpu-topology/rack=rack01
kubectl label node "${CLUSTER_NAME}-worker2" gpu-topology/nvlink-domain=rack01-domain01 gpu-topology/rack=rack01
kubectl label node "${CLUSTER_NAME}-worker3" gpu-topology/nvlink-domain=rack02-domain01 gpu-topology/rack=rack02

echo "=== Building and loading image ==="
make image IMAGE="$IMAGE_NAME"
kind load docker-image "$IMAGE_NAME" --name "$CLUSTER_NAME"

echo "=== Deploying scheduler ==="
kubectl apply -f deploy/configmap.yaml
kubectl apply -f deploy/deployment.yaml

echo "=== Waiting for scheduler to be ready ==="
kubectl -n kube-system wait --for=condition=available --timeout=60s deployment/gpu-topology-scheduler

echo "=== Done! Cluster '$CLUSTER_NAME' is ready ==="
echo ""
echo "Test with:"
echo "  kubectl apply -f hack/test-pods.yaml"
echo "  kubectl get pods -o wide"
