#!/usr/bin/env bash
set -euo pipefail

echo "=== DFW Azure Test Env Post-Apply ==="
echo "Resource group: $(terraform output -raw resource_group 2>/dev/null || echo 'dfw-test-rg')"

echo
echo "1. Get controller cluster credentials:"
echo "   $(terraform output -raw get_controller_kubeconfig 2>/dev/null || echo 'az aks get-credentials ...')"

echo
echo "2. ACR:"
terraform output -json acr 2>/dev/null || echo "See terraform output acr"

echo
echo "3. Zone CIDRs (use these exactly in your DFW Zone CRs):"
terraform output -json zone_vnet_cidrs 2>/dev/null || terraform output test_zones

echo
echo "4. Next: build images, push to ACR, helm install controller, apply CRs (Zones + Ground + ZoneRules), deploy agents."
echo "   Then run traffic between VMs in different zones and inspect DFW ringbuf + logs on the source and destination agents."
echo
echo "See ../../docs/plans/2026-06-04-azure-dfw-test-environment.md for the full validation matrix."
