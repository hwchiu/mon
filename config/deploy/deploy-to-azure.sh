#!/usr/bin/env bash
# Run with: DOCKER_HUB_REPO=youruser ./deploy-to-azure.sh
# Prerequisites: az login, terraform apply done, images pushed to Docker Hub, kubectl context for mgmt cluster.

set -euo pipefail

: "${DOCKER_HUB_REPO:?Set DOCKER_HUB_REPO e.g. export DOCKER_HUB_REPO=myuser}"
RESOURCE_GROUP="${RESOURCE_GROUP:-dfw-test-rg}"
MGMT_CLUSTER="${MGMT_CLUSTER:-dfw-zone-004}"

echo "=== Deploying DFW to Azure using Docker Hub images from ${DOCKER_HUB_REPO} ==="

echo "Applying CRD and namespace..."
kubectl apply -f ../crd/zone-crd.yaml || true
kubectl apply -f namespace.yaml

echo "Deploying controller (with UI on :8082) to ${MGMT_CLUSTER}..."
envsubst < controller-deployment.yaml | kubectl apply -f -
echo "   UI inside cluster: http://dfw-controller.dfw-system:8082"
echo "   From laptop: kubectl -n dfw-system port-forward svc/dfw-controller 8082:8082"

echo "Example for deploying agent DaemonSet to a zone cluster (repeat for each, set zone label):"
echo "  az aks get-credentials -g ${RESOURCE_GROUP} -n dfw-zone-001 --overwrite-existing"
echo "  DOCKER_HUB_REPO=${DOCKER_HUB_REPO} ZONE=zone-001 envsubst < agent-daemonset.yaml | kubectl apply -f -"

echo "For VMs (scp and run on each VM):"
echo "  export DOCKER_HUB_REPO=${DOCKER_HUB_REPO}"
echo "  bash vm-agent-example.sh zone-001 <mgmt-controller-ip>:9443"

echo "Then apply sample CRs:"
echo "  kubectl apply -f ../../config/samples/zone-dmz.yaml"
# etc, and ground rules

echo "Check: kubectl get pods -n dfw-system"
echo "See post-apply.sh and plan doc for full flow."
