package engine

import "time"

// CompiledPolicy is the result of the policy engine.
type CompiledPolicy struct {
	Version        string
	CreatedAt      time.Time
	MapData        []byte
	GroundHash     string
	ZoneRuleHash   string
}
