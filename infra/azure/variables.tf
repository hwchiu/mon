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
    # Simplified test topology (as requested):
    # - 1 AKS cluster (runs the DFW controller, in "Management" zone-004)
    # - Other VMs for installing/running the agents (in zone-001 and zone-002)
    #
    # The VMs are plain Linux hosts by default (Podman for standalone agent).
    # They also have optional k3s bootstrap + Cilium helper so you can test
    # deploying the agent as a DaemonSet inside a small k8s (with Cilium CNI)
    # for coexistence validation, without needing extra AKS clusters.
    #
    # Logical 4-zone model from the design is still supported by creating
    # additional Zone CRs + more VMs later if you want the full matrix tested.
    zone-001 = {
      name        = "DMZ"
      cidr        = "10.1.0.0/16"
      node_subnet = "10.1.1.0/24"
      vm_subnet   = "10.1.2.0/24"
      create_aks  = false
      create_vms  = 1   # VM for agents (zone-001)
    }
    zone-002 = {
      name        = "Internal"
      cidr        = "10.2.0.0/16"
      node_subnet = "10.2.1.0/24"
      vm_subnet   = "10.2.2.0/24"
      create_aks  = false
      create_vms  = 1   # VM for agents (zone-002)
    }
    zone-004 = {
      name        = "Management"
      cidr        = "10.4.0.0/16"
      node_subnet = "10.4.1.0/24"
      vm_subnet   = "10.4.2.0/24"
      create_aks  = true   # The single AKS that runs the controller
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
