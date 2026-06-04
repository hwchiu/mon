output "resource_group" {
  value = azurerm_resource_group.main.name
}

output "acr" {
  value = {
    login_server   = azurerm_container_registry.acr.login_server
    admin_username = azurerm_container_registry.acr.admin_username
  }
}

output "log_analytics" {
  value = azurerm_log_analytics_workspace.laws.name
}

output "controller_get_creds" {
  value = <<EOT
az aks get-credentials --resource-group ${azurerm_resource_group.main.name} --name ${try(azurerm_kubernetes_cluster.clusters["zone-004"].name, "dfw-zone-004")} --overwrite-existing
EOT
}

output "test_zones" {
  value = {
    for k, v in var.zones : k => {
      name = v.name
      cidr = v.cidr
      vnet = "dfw-${k}-vnet"
    }
  }
}

output "next_steps" {
  value = <<EOT
1. az group list | grep dfw-test
2. Run: terraform output -raw controller_get_creds
3. Build & push controller + agent images to the ACR above.
4. Create the 4 Zone CRs using the exact CIDRs shown in test_zones.
5. Deploy controller Helm chart to the Management AKS.
6. Deploy agent DaemonSet to the other AKS clusters (and run on the VMs via podman).
7. Apply ground + zone rules from docs/index.html examples.
8. Generate cross-zone traffic between VMs in different zones and inspect ringbuf/ logs on the agents.
EOT
}
