#!/usr/bin/env bash
set -euo pipefail

echo "=== DFW Azure Test Env Post-Apply ==="
echo "Resource group: $(terraform output -raw resource_group 2>/dev/null || echo 'dfw-test-rg')"

echo
echo "1. Get controller cluster credentials:"
echo "   $(terraform output -raw get_controller_kubeconfig 2>/dev/null || echo 'az aks get-credentials ...')"

echo
echo "2. Registry (ACR still in outputs, but using Docker Hub per your setup):"
echo "   DOCKER_HUB_REPO from your GitHub env / local: use ${DOCKER_HUB_REPO:-<set it>}/dfw-controller and /dfw-agent"
terraform output -json acr 2>/dev/null || echo "See terraform output acr (optional)"

echo
echo "3. Zone CIDRs (use these exactly in your DFW Zone CRs):"
terraform output -json zone_vnet_cidrs 2>/dev/null || terraform output test_zones

echo
echo "4. Next (using Docker Hub) - 1 AKS (controller) + 2 VMs (k3s+Cilium + Podman):"
echo "   On your logged-in server (with DOCKER_HUB_REPO exported):"
echo "     make docker-build-controller docker-build-agent"
echo "     docker push \${DOCKER_HUB_REPO}/dfw-controller:latest"
echo "     docker push \${DOCKER_HUB_REPO}/dfw-agent:latest"
echo "   Then use the helper:"
echo "     cd config/deploy"
echo "     ./deploy-to-azure.sh"
echo "   Or manually apply the yamls after envsubst."
echo "   The controller now serves a web UI on port 8082 (status + live config editor for zones/rules). Use port-forward to access it."
echo
echo "5. VMs have k3s pre-installed (single node). After ~2min: ssh in and run /root/install-cilium.sh to bring up Cilium CNI."
echo "   Copy k3s kubeconfig from VM (/etc/rancher/k3s/k3s.yaml), fix the server address to the VM IP, then you can deploy the DFW agent DaemonSet into the small k3s (tests Cilium coexistence - DFW only touches the admin-specified host iface)."
echo "   Podman is also there for the classic VM/Podman agent path (see vm-agent-example.sh)."
echo "6. Create the Zone CRs using the CIDRs (test_zones output). The 4-zone ground matrix from the design still applies for policy."
echo
echo "See ../../docs/plans/2026-06-04-azure-dfw-test-environment.md for the full validation matrix."
echo "GitHub Action .github/workflows/docker-build-push.yml will auto-build/push on commits using your DOCKER_HUB_* secrets."
