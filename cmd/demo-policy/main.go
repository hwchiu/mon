package main

import (
	"fmt"

	"github.com/hwchiu/mon/pkg/engine"
)

func main() {
	fmt.Println("DFW Policy Engine Demo - using the ground matrix from docs/index.html")

	zones := []engine.Zone{
		{ID: "zone-001", Name: "DMZ"},
		{ID: "zone-002", Name: "Internal"},
		{ID: "zone-003", Name: "Production"},
		{ID: "zone-004", Name: "Mgmt"},
	}

	// No zone rules
	p, _ := engine.CompileGroundAndZoneRules(nil, nil, zones)
	fmt.Printf("Compiled version: %s\n\n", p.Version)

	cases := []struct{ src, dst, port string }{
		{"zone-002", "zone-001", "443"},
		{"zone-001", "zone-002", "443"},
		{"zone-004", "zone-003", "22"},
		{"zone-003", "zone-003", "443"},
	}

	for _, c := range cases {
		v := engine.ComputeVerdict(c.src, c.dst, c.port, nil)
		fmt.Printf("Traffic %s -> %s:%s : %v (per ground matrix + both sides rule)\n", c.src, c.dst, c.port, v)
	}

	fmt.Println("\nTo open e.g. Internal->DMZ, add a ZoneRule that allows the ingress side at DMZ.")
}
