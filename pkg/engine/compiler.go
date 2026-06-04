package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hwchiu/mon/pkg/types"
)

// Zone, GroundRule, ZoneRule are simplified in-memory for compiler (bridge from CRs).
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

// Hardcoded ground matrix from design docs (zone name -> zone name -> "allow"/"deny").
// Ground is the immutable baseline; zone rules only upgrade deny->allow (OR).
var groundMatrix = map[string]map[string]string{
	"zone-001": {"zone-001": "allow", "zone-002": "deny", "zone-003": "deny", "zone-004": "deny"},
	"zone-002": {"zone-001": "allow", "zone-002": "allow", "zone-003": "deny", "zone-004": "deny"},
	"zone-003": {"zone-001": "deny", "zone-002": "deny", "zone-003": "allow", "zone-004": "deny"},
	"zone-004": {"zone-001": "allow", "zone-002": "allow", "zone-003": "allow", "zone-004": "allow"},
}

// zoneID assigns small uint32 ids for eBPF maps (1-based from "zone-NNN").
func zoneID(name string) uint32 {
	if strings.HasPrefix(name, "zone-") {
		n := strings.TrimPrefix(name, "zone-")
		if id, err := strconv.Atoi(n); err == nil {
			return uint32(id)
		}
	}
	// fallback hash-ish for unknown
	h := sha256.Sum256([]byte(name))
	return uint32(h[0]) | (uint32(h[1]) << 8) | (uint32(h[2]) << 16) | (uint32(h[3]) << 24)
}

// ComputeVerdict per design (both egress src_allows AND ingress dst_allows; ground wins; zone rules upgrade only).
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

// CompileGroundAndZoneRules implements the 3-stage Policy Engine (Collect -> Merge&Validate -> Serialize).
// Produces the exact 4 map structures the agents load (per docs/index.html + plan).
func CompileGroundAndZoneRules(groundRules []GroundRule, zoneRules []ZoneRule, zones []Zone) (*CompiledPolicy, error) {
	h := sha256.New()
	for _, g := range groundRules {
		h.Write([]byte(g.From + g.To + g.Port))
	}
	for _, z := range zoneRules {
		h.Write([]byte(z.SrcZone + z.DstZone + z.Port))
	}
	for _, zn := range zones {
		h.Write([]byte(zn.ID))
	}

	version := "v" + hex.EncodeToString(h.Sum(nil))[:12]

	// Build zone id map + zone_cidr entries (collect stage + serialize)
	zoneIDMap := make(map[string]uint32, len(zones))
	var zoneCIDRs []types.ZoneCIDREntry
	for _, z := range zones {
		id := zoneID(z.ID)
		zoneIDMap[z.ID] = id
		for _, c := range z.CIDRs {
			zoneCIDRs = append(zoneCIDRs, types.ZoneCIDREntry{CIDR: c, ZoneID: id})
		}
	}

	// Merge: start from ground matrix, apply zone rule upgrades (OR, only deny->allow)
	// For simplicity we materialize verdicts for all zone pairs + common ports + "all".
	// In full impl would also populate policy_rule_map for ranges/proto and prio.
	verdicts := make([]types.VerdictEntry, 0, len(zoneIDMap)*len(zoneIDMap)*4)
	portsToCheck := []uint16{0, 22, 80, 443, 53} // representative; "0" means all in key

	for srcName, srcID := range zoneIDMap {
		for dstName, dstID := range zoneIDMap {
			for _, pt := range portsToCheck {
				// Use the string ComputeVerdict (which applies ground + zonerules) to decide the effective.
				// Port as string for the func (it treats "0" or specific).
				portStr := "all"
				if pt != 0 {
					portStr = fmt.Sprintf("%d", pt)
				}
				allows := ComputeVerdict(srcName, dstName, portStr, zoneRules)
				verdicts = append(verdicts, types.VerdictEntry{
					Key: types.VerdictKey{
						SrcZone: srcID,
						DstZone: dstID,
						Port:    pt,
					},
					Allow: allows,
				})
			}
		}
	}

	// policy_rule_map stub: for now copy some verdicts as rules (ground prio low, zone higher)
	policyRules := make([]types.PolicyRuleEntry, 0, len(verdicts))
	for _, v := range verdicts {
		pr := types.PolicyRuleEntry{
			Key:    v.Key,
			Proto:  0, // all
			Action: v.Allow,
			Prio:   10,
		}
		policyRules = append(policyRules, pr)
	}

	// block_stats keys: the ones that can deny (for counters)
	var blockKeys []types.VerdictKey
	for _, v := range verdicts {
		if !v.Allow {
			blockKeys = append(blockKeys, v.Key)
		}
	}

	md := &types.MapData{
		Version:        version,
		ZoneCIDRs:      zoneCIDRs,
		Verdicts:       verdicts,
		PolicyRules:    policyRules,
		BlockStatsKeys: blockKeys,
	}

	return &CompiledPolicy{
		Version:        version,
		CreatedAt:      time.Now().UTC(),
		MapData:        md,
		GroundHash:     fmt.Sprintf("g-%x", sha256.Sum256([]byte(fmt.Sprintf("%v", groundRules))))[:12],
		ZoneRuleHash:   fmt.Sprintf("z-%x", sha256.Sum256([]byte(fmt.Sprintf("%v", zoneRules))))[:12],
	}, nil
}
