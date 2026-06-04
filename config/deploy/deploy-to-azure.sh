#!/usr/bin/env bash
# Run with: DOCKER_HUB_REPO=youruser ./deploy-to-azure.sh
# Prerequisites: az login, terraform apply done (now: 1 AKS controller + 2 k3s+Cilium VMs), images pushed, kubectl for the AKS.
# For the VMs' k3s: use their k3s.yaml (see post-apply.sh and script output).

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

echo "Example for deploying agent DaemonSet:"
echo "  # For the AKS (controller cluster itself, if you want agents there too):"
echo "  az aks get-credentials -g ${RESOURCE_GROUP} -n ${MGMT_CLUSTER} --overwrite-existing"
echo "  DOCKER_HUB_REPO=${DOCKER_HUB_REPO} ZONE=zone-004 envsubst < agent-daemonset.yaml | kubectl apply -f -"
echo
echo "  # For the k3s-on-VMs (the 'k8s with Cilium' test env on the 2 provisioned VMs):"
echo "  # 1. ssh to the VM, run /root/install-cilium.sh (after k3s ready)"
echo "  # 2. scp the k3s kubeconfig: scp azureuser@<VM-IP>:/etc/rancher/k3s/k3s.yaml ./k3s-zone-001.yaml"
echo "  # 3. Edit the yaml server: line to use the VM's private (or public) IP :6443"
echo "  # 4. KUBECONFIG=./k3s-zone-001.yaml DOCKER_HUB_REPO=... ZONE=zone-001 envsubst < agent-daemonset.yaml | kubectl apply -f -"
echo "     (repeat for zone-002 on the second VM). This tests DFW host agent + Cilium CNI coexistence."

echo "For pure VM / Podman agent path (no k8s on the VM):"
echo "  export DOCKER_HUB_REPO=${DOCKER_HUB_REPO}"
echo "  bash vm-agent-example.sh zone-001 <mgmt-controller-ip>:9443"

echo "Then apply sample CRs:"
echo "  kubectl apply -f ../../config/samples/zone-dmz.yaml"
# etc, and ground rules

echo "Check: kubectl get pods -n dfw-system"
echo "See post-apply.sh and plan doc for full flow."
