package agent

import (
	"context"
	"log"
	"sync"
)

// Config for the agent.
type Config struct {
	ControllerAddr string
	Zone           string
	Interfaces     string // comma separated
}

// Agent is the main DFW end-device agent.
type Agent struct {
	cfg    *Config
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// TODO: eBPF loader, maps, etc.
}

func New(cfg *Config) (*Agent, error) {
	return &Agent{cfg: cfg}, nil
}

func (a *Agent) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel

	a.wg.Add(1)
	go a.run(ctx)

	log.Printf("DFW agent started for zone %s (stub - real logic in later tasks)", a.cfg.Zone)
	return nil
}

func (a *Agent) run(ctx context.Context) {
	defer a.wg.Done()

	// TODO (per plan):
	// - Boot compile eBPF from source (LLVM)
	// - Load and attach to interfaces (tc-BPF)
	// - gRPC client: register, receive map data updates
	// - Signature verification
	// - Atomic map update (double buffer + active_idx)
	// - Ringbuf reader for audit
	// - Health / version reporting

	<-ctx.Done()
	log.Println("agent run loop stopped")
}

func (a *Agent) ApplyPolicy(version string, mapData []byte) error {
	log.Printf("Applying policy version %s (len=%d) - TODO: load into eBPF maps", version, len(mapData))
	// Real impl: deserialize mapData, update inactive maps, flip active_idx, etc.
	return nil
}

func (a *Agent) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
}
