package engine

import (
	"time"

	"github.com/hwchiu/mon/pkg/types"
)

// CompiledPolicy is the result of the policy engine (per design 3-stage: collect/merge/serialize).
type CompiledPolicy struct {
	Version        string
	CreatedAt      time.Time
	MapData        *types.MapData // now the real 4 maps (zone_cidr, policy_rule, verdict, block_stats)
	GroundHash     string
	ZoneRuleHash   string
}
