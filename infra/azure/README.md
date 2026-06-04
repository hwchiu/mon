# Azure DFW Test Environment (Terraform) - Simplified

**Current topology (as requested):** 

- **1 AKS cluster**: Runs *only* the DFW controller (placed in zone-004 / Management).
- **Other VMs**: For installing and running the agents (placed in zone-001 and zone-002).

The VMs are the environment where you install/run `dfw-agent` (primarily the standalone Podman path using `vm-agent-example.sh` or as a systemd service).

As mentioned earlier for "k8s with cilium configuration", the cloud-init on the VMs also includes optional k3s bootstrap + a `/root/install-cilium.sh` helper. This lets you test deploying the agent *as a DaemonSet inside k8s* (on the VM) while DFW protects only the specified host interface (proving clean coexistence with Cilium).

You declare the logical zones via `Zone` CRs using the CIDRs of the provisioned subnets/VMs. The full 4-zone design matrix can be used by creating the extra Zone CRs (and adding more VMs if needed).

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
