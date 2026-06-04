# mon — DFW (Distributed Firewall)

Centralized policy with decentralized eBPF enforcement using zones, ground rules, and zone rules (both-sides consent).

## Current Status
- Azure test infra provisioned (4 AKS + VMs in zones, ACR available).
- Docker images buildable (controller + agent with LLVM for runtime eBPF compile).
- GitHub Action for auto build/push to Docker Hub using `DOCKER_HUB_REPO` and `DOCKER_HUB_TOKEN` secrets.
- Go skeleton: CRDs (Zone, Ground/ZoneRule, PolicyVersion), controller reconciler + gRPC server, agent with gRPC client + policy apply stub, policy engine with exact design matrix logic + ComputeVerdict.
- Deployment helpers in `config/deploy/` and `infra/azure/scripts/`.

## Quick Start (after `cd infra/azure && terraform apply`)
1. On your server (docker logged in):
   ```bash
   export DOCKER_HUB_REPO=your-dockerhub
   make docker-build-controller docker-build-agent
   docker push ${DOCKER_HUB_REPO}/dfw-controller:latest
   docker push ${DOCKER_HUB_REPO}/dfw-agent:latest
   ```
   (Or let the GitHub Action do it on push.)

2. Get cluster creds:
   ```bash
   az aks get-credentials -g dfw-test-rg -n dfw-zone-004 --overwrite-existing
   ```

3. Deploy (customize yamls with your DOCKER_HUB_REPO):
   ```bash
   cd config/deploy
   ./deploy-to-azure.sh
   ```

4. Apply CRs (Zones with CIDRs from terraform output test_zones):
   ```bash
   kubectl apply -f ../../config/samples/zone-dmz.yaml
   # ground rules, zone rules per docs/index.html matrix
   ```

5. Access the controller frontend (status + live DFW config editor):
   ```bash
   kubectl -n dfw-system port-forward svc/dfw-controller 8082:8082
   # open http://localhost:8082
   # - See connected agents, zones, policy versions
   # - Interactive Ground Rules matrix (click cells to toggle allow/deny -> creates GroundRule CRs)
   # - Create Zones and ZoneRules via UI
   # - Changes will be picked up by controller -> new PolicyVersion -> pushed to agents
   ```

5. On VMs (for agent):
   Use `vm-agent-example.sh` with Docker Hub image and controller addr.

See `docs/plans/2026-06-04-azure-dfw-test-environment.md` and `config/deploy/`.

## Development
- `make build test`
- Docker: `make docker-build-*` (respects DOCKER_HUB_REPO)
- Code follows the implementation plan in `docs/plans/`.

## GitHub Secrets (for Actions)
- DOCKER_HUB_REPO
- DOCKER_HUB_TOKEN

(Already wired in .github/workflows/docker-build-push.yml)

## AFK Session Progress (auto continued)
- Infra: All 4 AKS + VMs + networking ready (verified via az and terraform).
- Docker: Builds verified, GH Action + Makefile updated for your DOCKER_HUB_* .
- Code: Agent now has gRPC client + Stream + ApplyPolicy hook; Controller has functional reconciler that compiles policy and creates PolicyVersion CR; Engine has full matrix + ComputeVerdict from design.
- Deploy: Updated scripts/yamls in config/deploy/ using ${DOCKER_HUB_REPO} (use envsubst or the deploy-to-azure.sh helper).
- Frontend: Controller now serves web UI on :8082 (status dashboard + agents + CRUD for Zones/GroundRules/ZoneRules that drive the engine + PolicyVersion). See pkg/frontend/ and updated deployment exposing port 8082.
- Added docker-compose.yml for local controller smoke test.
- CRD skeleton in config/crd/.

When back:
1. On server: export DOCKER_HUB_REPO=... ; make docker-build-controller docker-build-agent ; docker push ...
2. az aks get-credentials -g dfw-test-rg -n dfw-zone-004 ...
3. cd config/deploy ; ./deploy-to-azure.sh
4. kubectl apply -f ../../config/samples/... (edit for your zones)
5. Test cross-zone from VMs/ pods, watch agent logs/ringbuf.

See updated infra/azure/scripts/post-apply.sh and root README.

(Work done autonomously per your instruction.)
