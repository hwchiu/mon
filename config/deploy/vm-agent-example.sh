#!/bin/bash
# Example to run on the Azure VMs (after ssh)
# Assumes you have podman, and DOCKER_HUB_REPO exported or replaced.

set -euo pipefail

DOCKER_HUB_REPO="${DOCKER_HUB_REPO:-your-dockerhub-user}"
ZONE_ID="${1:-zone-001}"   # pass as arg, e.g. zone-001 for DMZ
CONTROLLER_ADDR="${2:-10.4.1.10:9443}"  # replace with actual controller service IP or LB in mgmt vnet

echo "Running DFW agent on VM for zone ${ZONE_ID}..."

sudo podman run -d \
  --name dfw-agent \
  --privileged \
  --network host \
  --pid host \
  -v /sys/fs/bpf:/sys/fs/bpf:rw \
  -v /var/lib/dfw-agent:/var/lib/dfw-agent \
  -v /lib/modules:/lib/modules:ro \
  -e DFW_ZONE="${ZONE_ID}" \
  -e DFW_CONTROLLER="${CONTROLLER_ADDR}" \
  -e DFW_INTERFACES="eth0" \
  "${DOCKER_HUB_REPO}/dfw-agent:latest"

echo "Agent started. Check logs with: sudo podman logs -f dfw-agent"
echo "On the VM, use 'ip addr' to confirm interfaces and IPs fall into the zone CIDR."
