# DFW — Distributed Firewall

**Centralized policy management with decentralized, kernel-level eBPF enforcement across Kubernetes clusters and bare-metal/VMs using a zone-centric zero-trust model.**

## Documentation

The primary, beautifully rendered documentation (including architecture, policy model, implementation roadmap, Azure test environment, and design review) lives on **GitHub Pages**:

→ **https://<your-org-or-user>.github.io/mon/** (or the Pages URL configured for this repo)

Source for the site is in the [`docs/`](./docs) folder:
- `index.html` — the main single-page site (Bootstrap + Mermaid diagrams)
- `plans/` — detailed implementation and Azure test environment plans
- `REVIEW-sdn-network-engineer.md` — independent senior network/SDN review

## Quick Links (from the site)

- [Implementation Plan](./docs/plans/2026-06-04-dfw-implementation-plan.md) (10-phase TDD roadmap)
- [Azure Test Environment](./docs/plans/2026-06-04-azure-dfw-test-environment.md) (Terraform + realistic multi-VNet validation topology)
- [Design Review](./docs/REVIEW-sdn-network-engineer.md) (gaps, recommendations, and validation priorities)

## Repository Structure

- `docs/` — GitHub Pages content (the living documentation)
- `infra/azure/` — Terraform for the multi-zone Azure test environment (4 VNets + AKS + VMs matching the DFW example zones)
- `bpf/`, `pkg/`, `cmd/`, `api/`, `charts/` — (to be populated by the implementation plan)

## Contributing

See the Implementation Plan and Design Review for current priorities and known gaps (especially bidirectional return-path semantics, zone CIDR handling with CNIs/overlays, tc attach ordering, and distribution scaling).

## License

TBD (currently internal design + tooling).

---

*This README is intentionally minimal. The real experience is the rendered site in `docs/index.html`.*
