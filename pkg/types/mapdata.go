package types

import (
	"net"
)

// MapData is the compiled, versioned data for the 4 eBPF maps.
// This is what gets serialized (or directly used with cilium/ebpf) and pushed to agents.
// Layouts are chosen to be directly loadable into the maps declared in bpf/dfw.bpf.c .
type MapData struct {
	Version string

	// ZoneCIDRs populates zone_cidr_map (LPM trie: IP/prefix -> zoneID).
	// Agents will insert these as LPM entries.
	ZoneCIDRs []ZoneCIDREntry

	// Verdicts populates verdict_map for the fast path: (srcZone, dstZone, port) -> allow?
	// Port 0 or special means "all" in some entries; exact port matches win or use ranges in policy_rule.
	Verdicts []VerdictEntry

	// PolicyRules populates policy_rule_map (more general ordered rules for fallback / ranges / proto).
	PolicyRules []PolicyRuleEntry

	// BlockStatsKeys are the keys we expect to count drops for in block_stats_map (ringbuf also used).
	// Value side is counter, incremented from eBPF.
	BlockStatsKeys []VerdictKey // reuse key shape
}

// ZoneCIDREntry for zone_cidr_map.
type ZoneCIDREntry struct {
	CIDR   string // e.g. "10.1.0.0/16"
	ZoneID uint32
}

// VerdictKey matches the C struct for verdict_map key (packed).
type VerdictKey struct {
	SrcZone uint32
	DstZone uint32
	Port    uint16 // 0 = all / wildcard in some contexts
	Pad     uint16
}

// VerdictEntry is a (key, allow) pair for the map.
type VerdictEntry struct {
	Key   VerdictKey
	Allow bool
}

// PolicyRuleEntry for policy_rule_map (more expressive).
type PolicyRuleEntry struct {
	Key    VerdictKey
	Proto  uint8 // 0=all, 6=tcp, 17=udp
	Action bool  // true allow
	Prio   uint8 // lower = higher priority (ground first)
}

// To allow easy hash for version etc.
func (m MapData) Summary() string {
	return m.Version
}

// Helper to parse CIDR for LPM (used by tests or agent).
func ParseCIDR(c string) (net.IP, *net.IPNet, error) {
	return net.ParseCIDR(c)
}
