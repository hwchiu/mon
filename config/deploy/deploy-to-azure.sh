#!/usr/bin/env bash
# Run with: DOCKER_HUB_REPO=youruser ./deploy-to-azure.sh
# Prerequisites: az login, terraform apply done (1 AKS for controller + VMs for agents), images pushed.
# Agents are installed on the VMs (Podman primary; optional k3s on VMs for Cilium coexistence test).

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

echo "Deploying agents (on the VMs, as requested - controller AKS is separate):"
echo "  # Primary: standalone agents on the VMs via Podman"
echo "  export DOCKER_HUB_REPO=${DOCKER_HUB_REPO}"
echo "  bash vm-agent-example.sh zone-001 <mgmt-controller-ip>:9443"
echo "  bash vm-agent-example.sh zone-002 <mgmt-controller-ip>:9443"
echo
echo "  # Optional k8s + Cilium path on the VMs (to test agent DaemonSet inside k8s without extra AKS):"
echo "  # 1. ssh to VM and run /root/install-cilium.sh (k3s is pre-bootstrapped)"
echo "  # 2. Copy k3s config from VM and deploy the agent DaemonSet into it (set ZONE correctly)."

echo "Note: The single AKS (${MGMT_CLUSTER}) only runs the controller. All agents go on the VMs."

echo "Then apply sample CRs:"
echo "  kubectl apply -f ../../config/samples/zone-dmz.yaml"
# etc, and ground rules

echo "Check: kubectl get pods -n dfw-system"
echo "See post-apply.sh and plan doc for full flow."
