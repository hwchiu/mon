package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hwchiu/mon/pkg/agent"
	pb "github.com/hwchiu/mon/pkg/proto/dfw/v1"
)

func main() {
	var (
		controllerAddr = flag.String("controller", "dfw-controller.dfw-system.svc.cluster.local:9443", "DFW controller gRPC address")
		zone           = flag.String("zone", "", "Zone ID this agent belongs to (e.g. zone-001)")
		interfaces     = flag.String("interfaces", "eth0", "Comma-separated list of host interfaces to protect")
		agentID        = flag.String("agent-id", "", "Agent ID (defaults to hostname)")
	)
	flag.Parse()

	if *zone == "" {
		log.Fatal("must specify --zone")
	}
	if *agentID == "" {
		*agentID, _ = os.Hostname()
	}

	log.Printf("DFW Agent starting - zone=%s, controller=%s, interfaces=%s, id=%s", *zone, *controllerAddr, *interfaces, *agentID)

	cfg := &agent.Config{
		ControllerAddr: *controllerAddr,
		Zone:           *zone,
		Interfaces:     *interfaces,
	}

	a, err := agent.New(cfg)
	if err != nil {
		log.Fatalf("failed to create agent: %v", err)
	}

	grpcClient, err := agent.NewGrpcClient(*controllerAddr, *zone, *agentID)
	if err != nil {
		log.Fatalf("failed to create grpc client: %v", err)
	}
	defer grpcClient.Close()

	if err := grpcClient.Register(); err != nil {
		log.Printf("register warning: %v", err)
	}

	// Start streaming updates in goroutine
	go func() {
		err := grpcClient.StreamUpdates(func(update *pb.PolicyUpdate) {
			log.Printf("Received policy update version=%s for zone=%s", update.Version, update.ZoneId)
			// TODO: verify signature, apply to eBPF maps atomically
			if err := a.ApplyPolicy(update.Version, update.MapData); err != nil {
				log.Printf("apply policy error: %v", err)
			}
		})
		if err != nil {
			log.Printf("stream error: %v", err)
		}
	}()

	if err := a.Start(); err != nil {
		log.Fatalf("agent start failed: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down agent...")
	a.Stop()
	time.Sleep(500 * time.Millisecond)
	fmt.Println("DFW agent exited cleanly")
}
