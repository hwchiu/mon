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
2. Run: terraform output -raw controller_get_creds   # 1 AKS (dfw-zone-004 / mgmt) for CONTROLLER only
3. Build & push controller + agent images (Docker Hub or ACR).
4. Create the 4 logical Zone CRs (use test_zones output - it shows all 4 from the design + which ones are actually provisioned with AKS/VMs right now).
5. Deploy controller to the single Management AKS (dfw-zone-004).
6. For the 2 VMs (zone-001 and zone-002):
   - They run k3s (single-node) + instructions for Cilium CNI (run /root/install-cilium.sh on the VM after boot).
   - Also have Podman pre-installed for the standalone "VM agent" path.
   - SSH to VM, get k3s kubeconfig: scp azureuser@<vm-ip>:/etc/rancher/k3s/k3s.yaml ./k3s-001.yaml (fix server IP if needed).
   - Then you can kubectl apply -f agent-daemonset.yaml (with ZONE=zone-001) into that k3s for "K8s + Cilium coexistence" test.
   - Or use the podman path: bash vm-agent-example.sh zone-001 <controller-ip>:9443
7. Apply ground + zone rules from docs/index.html (the 4x4 matrix).
8. Test: cross-zone traffic (VM1 <-> VM2, VM <-> AKS nodes, hostNet pods), check DFW drops/allows, ringbuf on agents, frontend UI on controller :8082.
EOT
}
