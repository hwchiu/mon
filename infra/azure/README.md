# Azure DFW Test Environment (Terraform) - Simplified

**Current topology (adjusted per request):** 1 AKS cluster (controller / management only, zone-004) + 2 Ubuntu VMs (zone-001 "DMZ" and zone-002 "Internal").

- The VMs are provisioned with:
  - Podman (for standalone "VM agent" path using the dfw-agent container).
  - k3s (single-node lightweight K8s) pre-installed with instructions + helper script to install Cilium CNI (`/root/install-cilium.sh`).
- This lets us test:
  - The Podman/VM agent path on bare Linux hosts whose IPs fall into the zone CIDRs.
  - "K8s with Cilium" path: deploy the agent DaemonSet *inside* the small k3s on the VM (hostNetwork + privileged + /sys/fs/bpf mount) while Cilium handles pod networking. DFW only attaches to the admin-specified host iface (eth0), proving coexistence (does not touch cilium_* veths or CNI interfaces).
- Logical 4 zones still supported via Zone CR `cidrs` (the other two zones can be empty or simulated later with more VMs).

See the full plan: `../../docs/plans/2026-06-04-azure-dfw-test-environment.md`

## Quick Start

```bash
cd infra/azure

# 1. Customize variables if needed (edit variables.tf or pass -var)
terraform init
terraform plan -out=tfplan
terraform apply tfplan

# 2. Get controller creds
$(terraform output -raw controller_get_creds)

# 3. Option A: Login to ACR (if using Azure Container Registry)
# az acr login --name <the-acr-name-from-output>

# Option B: Use Docker Hub (recommended, you have DOCKER_HUB_REPO and DOCKER_HUB_TOKEN set in GitHub)
# Locally (you mentioned logged in on this server):
# make docker-login   # or: echo "$DOCKER_HUB_TOKEN" | docker login -u "$DOCKER_HUB_REPO" --password-stdin
# Then build & push:
# make docker-push-controller docker-push-agent
# This uses DOCKER_HUB_REPO env var to tag as ${DOCKER_HUB_REPO}/dfw-controller and /dfw-agent

# 4. (Once images exist) If using Docker Hub, images will be at:
# ${DOCKER_HUB_REPO}/dfw-controller:latest and ${DOCKER_HUB_REPO}/dfw-agent:latest
# (The GitHub Action .github/workflows/docker-build-push.yml will automatically build & push on main/tags using your secrets)

# 5. Apply sample DFW CRs (Zones with the exact VNet CIDRs from terraform output "test_zones")
kubectl apply -f ../../config/samples/zones.yaml
# ground rules, zone rules, etc.

# 6. Deploy controller + agents (Helm or manifests once charts exist in the repo)
# helm upgrade --install dfw-controller ./charts/dfw-controller --set image.repository=... --set controller.grpc.endpoint=...

# 7. On the 2 VMs (ssh using the key):
#   - They have podman + k3s ready.
#   - For k3s + Cilium path: after boot (1-2 min) run `sudo /root/install-cilium.sh` on the VM.
#     Copy its k3s config (`scp azureuser@VM-IP:/etc/rancher/k3s/k3s.yaml .`), fix the server URL to the VM IP:6443.
#     Then `KUBECONFIG=... kubectl apply -f ../../config/deploy/agent-daemonset.yaml` (with ZONE=zone-001 etc.).
#   - For pure Podman/standalone agent: use the vm-agent-example.sh or the podman run command below.
#   - Find private IPs via Azure portal or `az vm list-ip-addresses`.

# 8. Validate using the scenarios in the plan doc (cross-zone traffic between the two VMs and/or the AKS nodes, watch DFW ringbuf/logs, use the controller UI at :8082 for live config).
```

**Important for DFW correctness**:
- The Zone CR `cidrs` you apply **must** contain the Azure VNet address_space (e.g. 10.1.0.0/16). This makes node and VM private IPs belong to the declared zone.
- Use the private IPs (not public) for test traffic so you exercise real VNet routing + zone lookup on the actual packet IPs seen by the host interfaces.

After validation, `terraform destroy` or delete the whole RG.

See the companion SDN design review: `../../docs/REVIEW-sdn-network-engineer.md` for what this env is intended to help discover and fix.
