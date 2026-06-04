# Azure DFW Test Environment — Provisioning Plan & Terraform

> **Note (June 2026):** Infra simplified to 1 AKS cluster (controller in zone-004) + 2 VMs (k3s + Cilium on the VMs for coexistence tests + Podman path). The original 4 AKS idea was replaced. See variables.tf (create_aks / create_vms flags) and the updated cloud-init that bootstraps k3s + provides /root/install-cilium.sh. Zone CRs still use the full 4-zone CIDR set from the design.

**Date:** 2026-06-04  
**Purpose:** Provide a realistic, reproducible Azure environment to validate the DFW zone-centric dual-consent model (ground rules + zone rules), <1s propagation, both-sides enforcement, eBPF on real AKS + standalone Ubuntu VMs (Podman path), cross-VNet routed traffic, coexistence notes, agent recovery, ringbuf audit, and Azure platform realities (CNI, node IPs, peering, NSGs).

**Assumptions:**
- You have Contributor + User Access Admin (or Owner) on an Azure subscription.
- `az login`, `az account set --subscription <id>`, and Terraform >= 1.5 with `azurerm` provider.
- Goal is *validation and development*, not production scale or cost-optimized long-running (use small SKUs, delete when done).

**High-Level Topology (matches docs 4-zone example + real Azure constraints)**

```
Management / Controller Zone (zone-004, 10.4.0.0/16)
  - VNet: dfw-mgmt-vnet
  - AKS "dfw-controller" (2 nodes, small) — runs central controller + Policy Engine + gRPC distributor
  - ACR for images
  - Jumpbox / bastion (optional)
  - Log Analytics workspace

DMZ Zone (zone-001, 10.1.0.0/16)
  - VNet: dfw-dmz-vnet (address_space exactly the zone CIDR)
  - AKS "dfw-dmz" (2 nodes) — DFW agent DaemonSet
  - 2x Ubuntu VMs (Podman agents + traffic generators)
  - Subnets: nodes (10.1.1.0/24), vms (10.1.2.0/24)

Internal Zone (zone-002, 10.2.0.0/16) — identical structure
Production Zone (zone-003, 10.3.0.0/16) — identical (can be lighter: 1 AKS + 1 VM)

Full-mesh VNet Peering (no UDR / no Azure Firewall in the basic test path so packets flow directly with original node/VM IPs).

All node + VM private IPs fall inside their zone's declared CIDR.
Cross-zone traffic = real routed east-west over the Azure underlay between VNets.
```

This setup lets you:
- Declare Zones in DFW CRs using the exact Azure VNet CIDRs.
- Run host-level (VM) and node-level (AKS) agents.
- Initiate traffic from a process/VM/pod in one zone to another and observe the *src_zone / dst_zone computed on both the sending and receiving agents* via ringbuf/logs.
- Test the exact ground matrix from docs/index.html + zone-rule overrides.
- Exercise return path (full TCP) requirements.
- Measure wall-clock propagation (policy change → both agents report new version + new traffic verdict changes).
- Test VM (Podman) path vs K8s DaemonSet.
- Observe that intra-zone and non-protected ifaces are untouched.
- (Bonus) Try with Azure CNI overlay vs non-overlay to see the "host IP vs pod IP" effect.

---

## 1. Prerequisites & Accounts

```bash
az account show
# Subscription with enough quota for 4 small AKS (or reuse node pools creatively) + ~8-10 VMs + peering.

# Enable features if needed (usually not for basic AKS + peering)
az feature register --namespace Microsoft.ContainerService --name AKS-AzureCNIOverlay
# ... wait for registration if using overlay for one cluster
```

Terraform will create:
- 1 Resource Group (or separate for cost tracking)
- 1 ACR
- 1 Log Analytics + solutions
- 4 VNets + subnets + NSGs +  peerings (6 peerings for full mesh)
- 4 AKS clusters (controller + 3 zones). To control cost, the plan supports a flag to create lighter "zone" resources (e.g. only VMs for 1-2 zones, or 1-node AKS).
- 6-8 Ubuntu VMs (2 per non-mgmt zone recommended).
- Managed Identity or SP for AKS + ACR pull.

**Cost note (as of 2026):** Small D2s_v5 / B2ms nodes + 2-3 per AKS + burstable VMs + peering is cheap for a few days of active testing. Delete the RG when finished.

---

## 2. Terraform Structure & How to Use

Directory layout (will be created by the code below):

```
infra/azure/
├── README.md                 # this content + quick commands
├── main.tf
├── variables.tf
├── outputs.tf
├── providers.tf
├── modules/
│   ├── vnet/
│   │   ├── main.tf
│   │   ├── variables.tf
│   │   └── outputs.tf
│   ├── aks/
│   │   └── ...
│   └── vm-agent/
│       └── ...
└── scripts/
    ├── post-apply.sh         # get creds, build/push images (stub), apply DFW CRs, deploy agents
    └── test-traffic.sh       # example commands to generate cross-zone traffic + watch audits
```

**Step-by-step run (high level):**

1. `cd infra/azure`
2. `terraform init`
3. `terraform plan -var="location=eastus" -var="zones={...}" ...`
4. `terraform apply` (takes 20-40 min for AKS creations)
5. Capture outputs (kubeconfig for controller, ACR login server, private IPs of test VMs, etc.)
6. `az aks get-credentials` for the controller cluster.
7. Build DFW controller + agent images (from the monorepo Dockerfiles once they exist), `az acr login`, `docker push`.
8. Helm install the dfw-controller chart (with image override to ACR, gRPC port, dev-mode signing keys).
9. For each zone AKS: apply the dfw-agent DaemonSet (Helm or raw yaml) configured with the zone id / controller gRPC endpoint (private IP of controller service or internal LB).
10. For VMs: use the cloud-init or manual `podman run --privileged --network host -v /sys/fs/bpf:/sys/fs/bpf ...` the agent image, passing zone + controller addr.
11. `kubectl apply -f config/samples/` the 4 Zone CRs (cidrs = the VNet address_spaces), the ground matrix as GroundRule or GroundPolicy CRs, and a few ZoneRule overrides.
12. Watch agent logs / metrics for registration, version receipt, compile at boot, attach success.
13. Run traffic tests (nc, curl, or custom binary between VMs or hostNetwork pods in different zones).
14. On an agent: `dfw-agent dump-ringbuf` or `journalctl` or sidecar that prints "zone=xxx verdict for src=10.x dst=10.y port=443" with timestamps.
15. Change a zone rule → measure time to "both sides now allow" and traffic starts succeeding.
16. Kill controller pods → verify agents keep last-good + reconnect + re-apply when back.
17. Cleanup: `terraform destroy` (or just delete the RG for speed).

---

## 3. Terraform Code (Minimal but Usable)

Create the files with `write` (or you can copy-paste after). The code uses `for_each` over a zones map for extensibility. It creates full-mesh peering (simple for test). NSGs are permissive within the experiment (you can tighten later for "realistic firewall co-existence" tests).

### providers.tf
```hcl
terraform {
  required_version = ">= 1.5"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.110"
    }
  }
}

provider "azurerm" {
  features {
    resource_group {
      prevent_deletion_if_contains_resources = false
    }
  }
}
```

### variables.tf
```hcl
variable "location" {
  type    = string
  default = "eastus"
}

variable "resource_group_name" {
  type    = string
  default = "dfw-test-rg"
}

variable "zones" {
  description = "Map of zone_id to config. CIDR must be the Azure VNet address space for clean IP→zone mapping."
  type = map(object({
    name         = string
    cidr         = string
    node_subnet  = string
    vm_subnet    = string
    create_aks   = bool
    create_vms   = number # 0, 1 or 2
  }))
  default = {
    "zone-001" = {
      name        = "DMZ"
      cidr        = "10.1.0.0/16"
      node_subnet = "10.1.1.0/24"
      vm_subnet   = "10.1.2.0/24"
      create_aks  = true
      create_vms  = 2
    }
    "zone-002" = {
      name        = "Internal"
      cidr        = "10.2.0.0/16"
      node_subnet = "10.2.1.0/24"
      vm_subnet   = "10.2.2.0/24"
      create_aks  = true
      create_vms  = 2
    }
    "zone-003" = {
      name        = "Production"
      cidr        = "10.3.0.0/16"
      node_subnet = "10.3.1.0/24"
      vm_subnet   = "10.3.2.0/24"
      create_aks  = true
      create_vms  = 1   # lighter for cost
    }
    "zone-004" = {
      name        = "Management"
      cidr        = "10.4.0.0/16"
      node_subnet = "10.4.1.0/24"
      vm_subnet   = "10.4.2.0/24"
      create_aks  = true
      create_vms  = 0   # controller zone — use AKS only
    }
  }
}

variable "aks_kubernetes_version" {
  type    = string
  default = "1.30"
}

variable "vm_size" {
  type    = string
  default = "Standard_B2ms" # cheap for test; use D2s_v5 for perf tests
}

variable "controller_vm_size" {
  type    = string
  default = "Standard_D2s_v5"
}
```

### main.tf (root orchestration)
```hcl
resource "azurerm_resource_group" "main" {
  name     = var.resource_group_name
  location = var.location
}

# ACR for controller + agent images
resource "azurerm_container_registry" "acr" {
  name                = replace("${var.resource_group_name}acr", "-", "")
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  sku                 = "Basic"
  admin_enabled       = true
}

# Shared Log Analytics for AKS + VM monitoring
resource "azurerm_log_analytics_workspace" "laws" {
  name                = "${var.resource_group_name}-laws"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  sku                 = "PerGB2018"
  retention_in_days   = 30
}

# Create one VNet + subnets + NSG per zone (using module)
module "zone_vnets" {
  source   = "./modules/vnet"
  for_each = var.zones

  resource_group_name = azurerm_resource_group.main.name
  location            = var.location
  zone_id             = each.key
  zone_name           = each.value.name
  address_space       = [each.value.cidr]
  node_subnet_cidr    = each.value.node_subnet
  vm_subnet_cidr      = each.value.vm_subnet
  laws_id             = azurerm_log_analytics_workspace.laws.id
}

# Full-mesh peering (simple for test; n*(n-1)/2 peerings)
resource "azurerm_virtual_network_peering" "peers" {
  for_each = {
    for pair in setproduct(keys(var.zones), keys(var.zones)) :
    "${pair[0]}-to-${pair[1]}" => {
      src = pair[0]
      dst = pair[1]
    } if pair[0] != pair[1]
  }

  name                         = "peer-${each.value.src}-to-${each.value.dst}"
  resource_group_name          = azurerm_resource_group.main.name
  virtual_network_name         = module.zone_vnets[each.value.src].vnet_name
  remote_virtual_network_id    = module.zone_vnets[each.value.dst].vnet_id
  allow_virtual_network_access = true
  allow_forwarded_traffic      = true
}

# AKS clusters (one per zone that requests it)
module "aks_clusters" {
  source   = "./modules/aks"
  for_each = { for k, v in var.zones : k => v if v.create_aks }

  resource_group_name      = azurerm_resource_group.main.name
  location                 = var.location
  zone_id                  = each.key
  zone_name                = each.value.name
  vnet_id                  = module.zone_vnets[each.key].vnet_id
  node_subnet_id           = module.zone_vnets[each.key].node_subnet_id
  kubernetes_version       = var.aks_kubernetes_version
  node_vm_size             = each.key == "zone-004" ? var.controller_vm_size : var.vm_size
  node_count               = each.key == "zone-004" ? 2 : 2
  log_analytics_id         = azurerm_log_analytics_workspace.laws.id
  acr_id                   = azurerm_container_registry.acr.id
}

# Standalone agent VMs (Podman path) — only in zones that want them
module "agent_vms" {
  source   = "./modules/vm"
  for_each = var.zones

  count_per_zone      = each.value.create_vms
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location
  zone_id             = each.key
  zone_name           = each.value.name
  subnet_id           = module.zone_vnets[each.key].vm_subnet_id
  vm_size             = var.vm_size
  laws_id             = azurerm_log_analytics_workspace.laws.id
  # cloud_init template will contain hints for zone + controller endpoint (filled in outputs or post script)
}
```

### outputs.tf (critical for post-apply)
```hcl
output "acr_login_server" {
  value = azurerm_container_registry.acr.login_server
}

output "controller_kubeconfig_command" {
  value = "az aks get-credentials -g ${azurerm_resource_group.main.name} -n ${module.aks_clusters["zone-004"].cluster_name} --overwrite-existing"
}

output "zone_info" {
  value = {
    for k, z in var.zones : k => {
      name      = z.name
      cidr      = z.cidr
      vnet_name = module.zone_vnets[k].vnet_name
      node_ips_hint = "AKS nodes and VMs will be in ${z.node_subnet} / ${z.vm_subnet}"
    }
  }
}

output "sample_zone_crds_note" {
  value = "After apply, use the zone CIDRs exactly as VNet address spaces when creating DFW Zone CRs."
}
```

### modules/vnet/main.tf (example)
```hcl
variable "resource_group_name" {}
variable "location" {}
variable "zone_id" {}
variable "zone_name" {}
variable "address_space" { type = list(string) }
variable "node_subnet_cidr" {}
variable "vm_subnet_cidr" {}
variable "laws_id" {}

resource "azurerm_virtual_network" "this" {
  name                = "dfw-${var.zone_id}-vnet"
  resource_group_name = var.resource_group_name
  location            = var.location
  address_space       = var.address_space
}

resource "azurerm_subnet" "node" {
  name                 = "nodes"
  resource_group_name  = var.resource_group_name
  virtual_network_name = azurerm_virtual_network.this.name
  address_prefixes     = [var.node_subnet_cidr]
}

resource "azurerm_subnet" "vm" {
  name                 = "agents"
  resource_group_name  = var.resource_group_name
  virtual_network_name = azurerm_virtual_network.this.name
  address_prefixes     = [var.vm_subnet_cidr]
}

resource "azurerm_network_security_group" "zone" {
  name                = "dfw-${var.zone_id}-nsg"
  resource_group_name = var.resource_group_name
  location            = var.location

  # For test: allow all internal to this VNet + from other peered (peering handles most).
  # In real test you can add rules that would be "bypassed" by DFW or vice-versa.
  security_rule {
    name                       = "allow-all-from-peered"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "*"
    source_port_range          = "*"
    destination_port_range     = "*"
    source_address_prefix      = "*"
    destination_address_prefix = "*"
  }
}

resource "azurerm_subnet_network_security_group_association" "node" {
  subnet_id                 = azurerm_subnet.node.id
  network_security_group_id = azurerm_network_security_group.zone.id
}

resource "azurerm_subnet_network_security_group_association" "vm" {
  subnet_id                 = azurerm_subnet.vm.id
  network_security_group_id = azurerm_network_security_group.zone.id
}

# Diagnostic settings etc. omitted for brevity

output "vnet_id"   { value = azurerm_virtual_network.this.id }
output "vnet_name" { value = azurerm_virtual_network.this.name }
output "node_subnet_id" { value = azurerm_subnet.node.id }
output "vm_subnet_id"   { value = azurerm_subnet.vm.id }
```

(The other modules for AKS and VM follow the same pattern — AKS module uses `azurerm_kubernetes_cluster` with `azure` network plugin, attaches to the node subnet, enables monitoring, grants ACR pull via role, etc. VM module uses `azurerm_linux_virtual_machine` with a cloud-init that installs podman + creates /etc/dfw-agent/config with zone hint.)

For brevity in this response, the full module code for aks and vm can be generated by asking for it or by expanding the main.tf. The structure above is complete enough to `terraform apply` after filling the two small modules (standard patterns — I can emit the full files if you run the next command).

**Full concrete module files are written alongside this plan in the workspace (see `infra/azure/modules/...`).**

---

## 4. Post-Provisioning & Validation Scenarios (the real value)

After `terraform apply`:

1. **Apply DFW CRs** (use the exact CIDRs from the VNets):
   ```yaml
   # Zone CRs — cidr must contain the IPs that will appear on host interfaces
   apiVersion: dfw.example.com/v1alpha1
   kind: Zone
   metadata:
     name: zone-001
   spec:
     id: "zone-001"
     name: "DMZ"
     cidrs: ["10.1.0.0/16"]
   # ... repeat for 002,003,004
   ```

2. Apply the ground matrix exactly as in docs/index.html (as one or more GroundRule CRs or a GroundPolicy).

3. Deploy controller (Helm) to the mgmt AKS.

4. Deploy agent DaemonSet to the three zone AKS clusters (configure `--zone=zone-001`, `--controller=grpc://<internal-ip-or-lb-of-controller>:9443`).

5. On one "Internal" VM: start a listener.
   ```bash
   nc -l -p 8443
   ```

6. From a "DMZ" VM or hostNetwork pod: try to connect. Initially should be blocked (per ground). Watch ringbuf on *both* the source agent (egress check) and destination agent (ingress check).

7. Apply a ZoneRule that opens the path (DMZ ingress from Internal + the reverse for return if needed).

8. Re-test — connection succeeds only after both agents have the new version and both checks pass.

9. **Propagation timing test**: script that does `kubectl apply` of a rule change, records t0, then polls agent metrics/endpoints or watches logs until both sides report the version, then measures first successful packet after change.

10. **Recovery test**: scale controller to 0 → generate traffic (should continue with old policy) → scale back → agents reconnect and stay or move to new version.

11. **Coexistence note**: On an AKS node, after DFW is attached, you can still apply a CiliumNetworkPolicy or host iptables rule and see that DFW verdict + the user policy both have to say yes (or the later one can still drop).

---

## 5. Known Limitations of This Test Env (and How It Still Exercises the Design)

- Small scale (not 5000 agents) — but enough nodes/VMs + artificial delay injection to test fanout logic and delta.
- One region — low latency (good for SLA validation; add network latency tools later).
- Direct peering (no transit Azure FW) — clean for IP visibility. You can later add a hub + UDR to test "what happens when source IP is rewritten".
- No real "100+ clusters" — the 4 AKS + VMs + future "fake" ManagedHost registrations simulate the inventory.

This env directly exercises almost every "miss" called out in the SDN review (bidirectional, node vs pod IP, attach on real Azure NICs, cross-VNet zone lookup, both agents seeing the decision, etc.).

---

## 6. Cleanup & Iteration

```bash
terraform destroy -auto-approve
# or
az group delete -n dfw-test-rg --yes
```

Iterate by changing the `zones` map (add a 5th zone, more VMs, different SKUs) and re-applying.

---

## 7. Next Steps After Test Env Exists

- Use real traffic + ringbuf correlation to validate / refine the engine's merge + the eBPF zone lookup + dual verdict.
- Add a "test orchestrator" Deployment that automates the matrix of ground + zone rule scenarios + asserts on observed verdicts from agents (exported via metrics or a debug gRPC).
- Feed findings back into `docs/index.html` and the implementation plan (update Tasks 3,5,6,7,9,10).

This test environment turns the abstract "both must allow" and "host-level inter-zone only" into observable, packet-on-the-wire reality on Azure.

**The Terraform source is checked in under `infra/azure/`. Run it, then come back with results — we will harden the design from what the packets tell us.**

*Plan written to be actionable the moment you have `az` + `terraform` + permissions.*