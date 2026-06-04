variable "location" {
  description = "Azure region"
  type        = string
  default     = "eastus"
}

variable "resource_group_name" {
  type    = string
  default = "dfw-test-rg"
}

variable "zones" {
  description = "Zone definitions. CIDR = Azure VNet address_space so node/VM IPs fall inside the declared zone for DFW lookup."
  type = map(object({
    name        = string
    cidr        = string
    node_subnet = string
    vm_subnet   = string
    create_aks  = bool
    create_vms  = number
  }))
  default = {
    # Logical zones per design (CIDRs used for Zone CRs + DFW map population).
    # Infra reality (simplified per request): 1 AKS (controller only in zone-004 / mgmt),
    # + 2 VMs (k3s + Cilium single-node "clusters" + Podman for standalone agent path).
    # zone-001 and zone-002 get 1 VM each (in their vm_subnet).
    # zone-003 and zone-004 have no extra VMs (use AKS nodes for zone-004 if desired).
    # You can add more VMs or AKS later by flipping the create_* flags (will create additional VNets).
    zone-001 = {
      name        = "DMZ"
      cidr        = "10.1.0.0/16"
      node_subnet = "10.1.1.0/24"
      vm_subnet   = "10.1.2.0/24"
      create_aks  = false
      create_vms  = 1
    }
    zone-002 = {
      name        = "Internal"
      cidr        = "10.2.0.0/16"
      node_subnet = "10.2.1.0/24"
      vm_subnet   = "10.2.2.0/24"
      create_aks  = false
      create_vms  = 1
    }
    zone-003 = {
      name        = "Production"
      cidr        = "10.3.0.0/16"
      node_subnet = "10.3.1.0/24"
      vm_subnet   = "10.3.2.0/24"
      create_aks  = false
      create_vms  = 0
    }
    zone-004 = {
      name        = "Management"
      cidr        = "10.4.0.0/16"
      node_subnet = "10.4.1.0/24"
      vm_subnet   = "10.4.2.0/24"
      create_aks  = true # Controller lives here
      create_vms  = 0
    }
  }
}

variable "kubernetes_version" {
  type    = string
  default = "1.34"
}

variable "vm_size" {
  type    = string
  default = "Standard_B2ms"
}

variable "controller_vm_size" {
  type    = string
  default = "Standard_D2_v4"
}
