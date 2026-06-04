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
  # Returns *all* 4 logical zones from the design (needed for full ground matrix + Zone CRs).
  # The "provisioned" field tells you whether we actually created AKS/VMs for it in this run.
  value = {
    for k, v in var.zones : k => {
      name        = v.name
      cidr        = v.cidr
      provisioned = v.create_aks || (v.create_vms > 0)
    }
  }
}

output "next_steps" {
  value = <<EOT
1. az group list | grep dfw-test
2. Run: terraform output -raw controller_get_creds   # ONLY 1 AKS (dfw-zone-004) for the CONTROLLER
3. Build & push images.
4. Create Zone CRs using CIDRs from "terraform output test_zones" (defines the zones we actually provision: 001+002 for agent VMs, 004 for controller AKS).
5. Deploy controller to the single AKS.
6. On the VMs ("other VMs which install the agents"):
   - Primary: use the Podman path → bash vm-agent-example.sh <zone> <controller-ip>:9443
   - Optional (for k8s + Cilium coexistence test, as mentioned earlier): VMs have k3s pre-installed. After boot run /root/install-cilium.sh, copy k3s kubeconfig from the VM, then deploy agent-daemonset.yaml into that small k3s (set the ZONE env correctly). DFW agent protects the host eth0 while Cilium handles the pods.
7. Apply ground + zone rules (design matrix).
8. Test cross "zone" traffic between the VMs and to the controller AKS. Inspect agent behavior + use controller UI (:8082).
EOT
}
