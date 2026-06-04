package engine

import (
	"testing"
)

func TestComputeVerdictGroundMatrix(t *testing.T) {
	// Test the exact matrix from docs/index.html
	tests := []struct {
		src, dst, port string
		want           bool
	}{
		// Self zones are allowed in ground
		{"zone-001", "zone-001", "443", true},
		{"zone-002", "zone-002", "443", true},
		{"zone-003", "zone-003", "22", true},
		{"zone-004", "zone-004", "22", true},
		// Cross that are denied by the matrix (one side or both deny)
		{"zone-001", "zone-002", "443", false},
		{"zone-003", "zone-001", "443", false},
		// Mgmt to others are allowed in Mgmt row, but the dest check uses the other row which is deny for non-Mgmt
		// So default false unless zone rule opens the dest ingress
		{"zone-004", "zone-001", "443", false},
	}

	for _, tt := range tests {
		got := ComputeVerdict(tt.src, tt.dst, tt.port, nil)
		if got != tt.want {
			t.Errorf("ComputeVerdict(%s,%s,%s) = %v, want %v", tt.src, tt.dst, tt.port, got, tt.want)
		}
	}
}

func TestCompileProducesVersion(t *testing.T) {
	zones := []Zone{{ID: "zone-001", Name: "DMZ", CIDRs: []string{"10.1.0.0/16"}}}
	p, err := CompileGroundAndZoneRules(nil, nil, zones)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version == "" {
		t.Error("expected version")
	}
}
