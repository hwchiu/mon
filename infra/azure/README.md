# Azure DFW Test Environment (Terraform)

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

# 3. Login to ACR (username + password from portal or `az acr credential show`)
az acr login --name <the-acr-name-from-output>

# 4. (Once images exist) build & push
# docker build -t <acr>/dfw-controller:latest -f Dockerfile.controller .
# docker push ...
# same for agent image (must contain clang/LLVM + the bpf/ C source + the Go agent binary)

# 5. Apply sample DFW CRs (Zones with the exact VNet CIDRs from terraform output "test_zones")
kubectl apply -f ../../config/samples/zones.yaml
# ground rules, zone rules, etc.

# 6. Deploy controller + agents (Helm or manifests once charts exist in the repo)
# helm upgrade --install dfw-controller ./charts/dfw-controller --set image.repository=... --set controller.grpc.endpoint=...

# 7. On the standalone VMs (ssh using the key you configured):
# Find their private IPs (az network nic list ... or portal)
# Then run the agent container (example once the image is ready):
# sudo podman run --rm --privileged --network host \
#   -v /sys/fs/bpf:/sys/fs/bpf:rw \
#   -v /var/lib/dfw-agent:/var/lib/dfw-agent \
#   -e DFW_ZONE=zone-001 \
#   -e DFW_CONTROLLER=10.4.x.x:9443 \
#   <acr>/dfw-agent:latest

# 8. Validate using the scenarios in the plan doc (cross zone nc/curl + watch agent ringbuf/logs on both ends).
```

**Important for DFW correctness**:
- The Zone CR `cidrs` you apply **must** contain the Azure VNet address_space (e.g. 10.1.0.0/16). This makes node and VM private IPs belong to the declared zone.
- Use the private IPs (not public) for test traffic so you exercise real VNet routing + zone lookup on the actual packet IPs seen by the host interfaces.

After validation, `terraform destroy` or delete the whole RG.

See the companion SDN design review: `../../docs/REVIEW-sdn-network-engineer.md` for what this env is intended to help discover and fix.
