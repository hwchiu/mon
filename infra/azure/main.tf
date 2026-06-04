resource "azurerm_resource_group" "main" {
  name     = var.resource_group_name
  location = var.location
}

resource "azurerm_container_registry" "acr" {
  name                = replace("${var.resource_group_name}acr", "-", "")
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  sku                 = "Basic"
  admin_enabled       = true
}

resource "azurerm_log_analytics_workspace" "laws" {
  name                = "${var.resource_group_name}-laws"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  sku                 = "PerGB2018"
  retention_in_days   = 30
}

# Dynamically select a currently supported Kubernetes version in the region
# Using pinned version from variable for compatibility in this subscription/region

# VNets/subnets/NSG only for zones that actually get resources.
# Current defaults (per request): 
#   - zone-004: 1 AKS (controller only)
#   - zone-001 and zone-002: 1 VM each (for installing/running the agents)
# No zone-003 by default (add it manually if you want the full 4-zone design matrix with extra VMs).
locals {
  active_zones = { for k, v in var.zones : k => v if v.create_aks || (v.create_vms > 0) }
  zone_keys    = keys(local.active_zones)
}

resource "azurerm_virtual_network" "zones" {
  for_each = local.active_zones

  name                = "dfw-${each.key}-vnet"
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location
  address_space       = [each.value.cidr]
}

resource "azurerm_subnet" "nodes" {
  for_each = local.active_zones

  name                 = "nodes"
  resource_group_name  = azurerm_resource_group.main.name
  virtual_network_name = azurerm_virtual_network.zones[each.key].name
  address_prefixes     = [each.value.node_subnet]
}

resource "azurerm_subnet" "agents" {
  for_each = local.active_zones

  name                 = "agents"
  resource_group_name  = azurerm_resource_group.main.name
  virtual_network_name = azurerm_virtual_network.zones[each.key].name
  address_prefixes     = [each.value.vm_subnet]
}

resource "azurerm_network_security_group" "zone" {
  for_each = local.active_zones

  name                = "dfw-${each.key}-nsg"
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location

  security_rule {
    name                       = "allow-all-test"
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

resource "azurerm_subnet_network_security_group_association" "nodes" {
  for_each = local.active_zones

  subnet_id                 = azurerm_subnet.nodes[each.key].id
  network_security_group_id = azurerm_network_security_group.zone[each.key].id
}

resource "azurerm_subnet_network_security_group_association" "agents" {
  for_each = local.active_zones

  subnet_id                 = azurerm_subnet.agents[each.key].id
  network_security_group_id = azurerm_network_security_group.zone[each.key].id
}

# Full mesh peering among active zones (only the ones with resources)
resource "azurerm_virtual_network_peering" "mesh" {
  for_each = {
    for pair in setproduct(local.zone_keys, local.zone_keys) :
    "${pair[0]}-to-${pair[1]}" => {
      src = pair[0]
      dst = pair[1]
    } if pair[0] != pair[1]
  }

  name                         = "peer-${each.key}"
  resource_group_name          = azurerm_resource_group.main.name
  virtual_network_name         = azurerm_virtual_network.zones[each.value.src].name
  remote_virtual_network_id    = azurerm_virtual_network.zones[each.value.dst].id
  allow_virtual_network_access = true
  allow_forwarded_traffic      = true
}

# AKS clusters - only for zones where create_aks = true (currently only zone-004 / Management for the controller).
# Other zones are logical only (their CIDRs are still needed for the full 4-zone ground matrix in the design).
resource "azurerm_kubernetes_cluster" "clusters" {
  for_each = { for k, v in var.zones : k => v if v.create_aks }

  name                = "dfw-${each.key}"
  location            = var.location
  resource_group_name = azurerm_resource_group.main.name
  dns_prefix          = "dfw-${each.key}"
  kubernetes_version  = var.kubernetes_version

  default_node_pool {
    name           = "system"
    node_count     = each.key == "zone-004" ? 2 : 2
    vm_size        = each.key == "zone-004" ? var.controller_vm_size : var.vm_size
    vnet_subnet_id = azurerm_subnet.nodes[each.key].id
  }

  identity {
    type = "SystemAssigned"
  }

  # Match existing imported clusters to avoid update errors on OIDC
  oidc_issuer_enabled = true

  network_profile {
    network_plugin = "azure"
    service_cidr   = cidrsubnet(each.value.cidr, 8, 200) # e.g. 10.1.200.0/24 inside the zone cidr
    dns_service_ip = cidrhost(cidrsubnet(each.value.cidr, 8, 200), 10)
  }

  oms_agent {
    log_analytics_workspace_id = azurerm_log_analytics_workspace.laws.id
  }

  depends_on = [azurerm_virtual_network_peering.mesh]
}

# Grant AKS pull from ACR (role assignment)
resource "azurerm_role_assignment" "aks_acr" {
  for_each = azurerm_kubernetes_cluster.clusters

  scope                = azurerm_container_registry.acr.id
  role_definition_name = "AcrPull"
  principal_id         = each.value.kubelet_identity[0].object_id
}

# Standalone Ubuntu VMs for Podman agents (in zones that request them)
resource "azurerm_linux_virtual_machine" "agent_vms" {
  for_each = {
    for combo in flatten([
      for zk, zv in var.zones : [
        for i in range(zv.create_vms) : {
          key      = "${zk}-vm${i}"
          zone_key = zk
          index    = i
        }
      ]
    ]) : combo.key => combo
  }

  name                = "dfw-${each.value.zone_key}-agent-${each.value.index}"
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location
  size                = var.vm_size
  admin_username      = "azureuser"

  network_interface_ids = [
    azurerm_network_interface.agent_nic[each.key].id
  ]

  admin_ssh_key {
    username   = "azureuser"
    public_key = file("~/.ssh/id_rsa.pub") # change or use variable / tls_private_key
  }

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "Standard_LRS"
  }

  source_image_reference {
    publisher = "Canonical"
    offer     = "0001-com-ubuntu-server-jammy"
    sku       = "22_04-lts"
    version   = "latest"
  }

  custom_data = base64encode(templatefile("${path.module}/cloud-init-agent.yaml", {
    zone_id   = each.value.zone_key
    zone_name = var.zones[each.value.zone_key].name
    # Controller endpoint will be filled post-apply (internal LB or pod IP in mgmt)
    controller = "dfw-controller.dfw-system.svc.cluster.local:9443"
  }))

  depends_on = [azurerm_virtual_network_peering.mesh]
}

resource "azurerm_network_interface" "agent_nic" {
  for_each = {
    for combo in flatten([
      for zk, zv in var.zones : [
        for i in range(zv.create_vms) : {
          key      = "${zk}-vm${i}"
          zone_key = zk
        }
      ]
    ]) : combo.key => combo
  }

  name                = "dfw-${each.value.zone_key}-nic-${each.value.key}"
  location            = var.location
  resource_group_name = azurerm_resource_group.main.name

  ip_configuration {
    name                          = "internal"
    subnet_id                     = azurerm_subnet.agents[each.value.zone_key].id
    private_ip_address_allocation = "Dynamic"
  }
}

# Outputs for convenience
output "acr_login_server" {
  value = azurerm_container_registry.acr.login_server
}

output "controller_cluster_name" {
  value = try(azurerm_kubernetes_cluster.clusters["zone-004"].name, "n/a")
}

output "get_controller_kubeconfig" {
  value = <<EOT
az aks get-credentials -g ${azurerm_resource_group.main.name} -n ${try(azurerm_kubernetes_cluster.clusters["zone-004"].name, "dfw-zone-004")} --overwrite-existing
EOT
}

output "zone_vnet_cidrs" {
  value = { for k, v in var.zones : k => v.cidr }
}

output "agent_vm_private_ips_hint" {
  value = "Use az vm list-ip-addresses or the portal to find the private IPs of the agent VMs. They live inside the zone CIDRs."
}
