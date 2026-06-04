package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Zone, GroundRule, ZoneRule are simplified in-memory for compiler.
type Zone struct {
	ID    string
	Name  string
	CIDRs []string
}

type GroundRule struct {
	From string
	To   string
	Port string
}

type ZoneRule struct {
	SrcZone string
	DstZone string
	Port    string
}

// Uses CompiledPolicy from engine.go

// Hardcoded ground matrix from design docs.
var groundMatrix = map[string]map[string]string{
	"zone-001": {"zone-001": "allow", "zone-002": "deny", "zone-003": "deny", "zone-004": "deny"},
	"zone-002": {"zone-001": "allow", "zone-002": "allow", "zone-003": "deny", "zone-004": "deny"},
	"zone-003": {"zone-001": "deny", "zone-002": "deny", "zone-003": "allow", "zone-004": "deny"},
	"zone-004": {"zone-001": "allow", "zone-002": "allow", "zone-003": "allow", "zone-004": "allow"},
}

// ComputeVerdict per design.
func ComputeVerdict(srcZone, dstZone, port string, activeZoneRules []ZoneRule) bool {
	srcGround := getGround(srcZone, dstZone)
	dstGround := getGround(dstZone, srcZone)

	srcAllows := srcGround == "allow"
	dstAllows := dstGround == "allow"

	for _, zr := range activeZoneRules {
		if zr.SrcZone == srcZone && zr.DstZone == dstZone && (zr.Port == port || zr.Port == "all") {
			srcAllows = true
		}
		if zr.SrcZone == dstZone && zr.DstZone == srcZone && (zr.Port == port || zr.Port == "all") {
			dstAllows = true
		}
	}
	return srcAllows && dstAllows
}

func getGround(src, dst string) string {
	if m, ok := groundMatrix[src]; ok {
		if a, ok := m[dst]; ok {
			return a
		}
	}
	return "deny"
}

// CompileGroundAndZoneRules ...
func CompileGroundAndZoneRules(groundRules []GroundRule, zoneRules []ZoneRule, zones []Zone) (*CompiledPolicy, error) {
	h := sha256.New()
	for _, g := range groundRules { h.Write([]byte(g.From + g.To + g.Port)) }
	for _, z := range zoneRules { h.Write([]byte(z.SrcZone + z.DstZone + z.Port)) }
	for _, zn := range zones { h.Write([]byte(zn.ID)) }

	version := "v" + hex.EncodeToString(h.Sum(nil))[:12]

	// Stub map data: in real, this would be the binary for eBPF maps.
	// For demo, include some verdict examples.
	mapData := []byte(fmt.Sprintf("DFW-MAP v=%s zones=%d compute-example: internal->dmz-443=%v", version, len(zones), ComputeVerdict("zone-002", "zone-001", "443", zoneRules)))

	return &CompiledPolicy{
		Version:        version,
		CreatedAt:      time.Now().UTC(),
		MapData:        mapData,
		GroundHash:     "ground-stub",
		ZoneRuleHash:   "zonerules-stub",
	}, nil
}
