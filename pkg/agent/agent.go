package agent

import (
	"context"
	"encoding/json"
	"log"
	"os/exec"
	"sync"

	"github.com/hwchiu/mon/pkg/types"
)

// Config for the agent.
type Config struct {
	ControllerAddr string
	Zone           string
	Interfaces     string // comma separated, e.g. "eth0"
}

// Agent is the main DFW end-device agent.
type Agent struct {
	cfg    *Config
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// In real: loaded *ebpf.Collection, the 4 maps, ringbuf reader, active state.
}

func New(cfg *Config) (*Agent, error) {
	return &Agent{cfg: cfg}, nil
}

func (a *Agent) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel

	a.wg.Add(1)
	go a.run(ctx)

	log.Printf("DFW agent started for zone %s (interfaces: %s)", a.cfg.Zone, a.cfg.Interfaces)
	// In real container (with NET_ADMIN etc): compile eBPF C, load, attach tc to ifaces, start ringbuf goroutine.
	return nil
}

func (a *Agent) run(ctx context.Context) {
	defer a.wg.Done()

	// Boot-time: compile the shipped bpf/dfw.bpf.c using LLVM (data-driven, never push bytecode).
	// This would be: exec.Command("clang", "-O2", "-target", "bpf", ... "bpf/dfw.bpf.c", "-o", "/tmp/dfw.bpf.o")
	// Then use cilium/ebpf to load the ELF, pin maps under /sys/fs/bpf/dfw/..., attach to tc on cfg.Interfaces.
	if err := a.compileEBPF(); err != nil {
		log.Printf("eBPF compile note (will be full in privileged agent container): %v", err)
	}

	// TODO real: load collection, attach, start apply loop + ringbuf consumer that reports via gRPC or logs.

	<-ctx.Done()
	log.Println("agent run loop stopped")
}

// compileEBPF runs the LLVM step at "boot" (stub that succeeds if clang present in PATH).
func (a *Agent) compileEBPF() error {
	// The real command (see Dockerfile.agent + plan): clang -O2 -g -target bpf -c bpf/dfw.bpf.c -o /var/lib/dfw/dfw.bpf.o
	cmd := exec.Command("clang", "-O2", "-target", "bpf", "-c", "bpf/dfw.bpf.c", "-o", "/tmp/dfw.bpf.o")
	if err := cmd.Run(); err != nil {
		return err // expected in non-agent dev envs
	}
	log.Printf("DFW eBPF C compiled successfully at boot (data-driven per design)")
	return nil
}

func (a *Agent) ApplyPolicy(version string, mapData []byte) error {
	log.Printf("Applying policy version %s (wire len=%d)", version, len(mapData))

	var md types.MapData
	if err := json.Unmarshal(mapData, &md); err != nil {
		// If not json (old stub), just log
		log.Printf("  (note: mapData not json MapData; using as opaque for skeleton)")
		return nil
	}

	// Real impl here (using github.com/cilium/ebpf):
	// 1. Get the maps from the loaded collection by name: zone_cidr_map, verdict_map, etc.
	// 2. For each in md.ZoneCIDRs: parse CIDR, create lpm key, map.Update(key, zoneID, ebpf.UpdateAny)
	// 3. Same for Verdicts -> verdict_map.Update( VerdictKey{...}, allow ? 1:0 , ...)
	// 4. After populating the "inactive" view (or just update since hash), flip the active_idx in config_map.
	// 5. Persist last-known-good (version + map bytes or just the active state) for recovery.
	log.Printf("  would load into 4 eBPF maps: zoneCIDRs=%d verdicts=%d policyRules=%d blockKeys=%d",
		len(md.ZoneCIDRs), len(md.Verdicts), len(md.PolicyRules), len(md.BlockStatsKeys))
	log.Printf("  (double-buf active_idx flip + tc attach would make enforcement live for zone %s)", a.cfg.Zone)

	// For demo: report applied back (in real the gRPC client would call ReportStatus after successful apply).
	return nil
}

func (a *Agent) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
}
