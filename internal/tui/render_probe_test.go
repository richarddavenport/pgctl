package tui

import (
	"testing"
	"time"

	"github.com/richarddavenport/pgctl/internal/engine"
)

// TestRenderProbe prints a populated frame so a human can look at the layout.
// Run with -v; it asserts nothing a reader could not see.
func TestRenderProbe(t *testing.T) {
	m := model(t)
	withSnapshot(t, m)
	m.now = time.Now()
	m.probes["prd"] = &engine.Probe{
		Connection: "prd", Reachable: true, ServerVersion: 170004,
		Host: "prd.example", Port: 5432, User: "mbpiadmin", ProbedAt: time.Now(),
		Databases: []engine.DatabaseInfo{
			{Name: "product-development", Bytes: 11 << 30},
			{Name: "claims", Bytes: 48 << 20},
			{Name: "quote", Bytes: 20 << 30},
		},
	}
	m.probes["qat"] = &engine.Probe{Connection: "qat", Reachable: true, ServerVersion: 170004,
		Databases: []engine.DatabaseInfo{{Name: "product-development", Bytes: 9 << 30}}}
	m.probes["scratch"] = &engine.Probe{Connection: "scratch", Err: errUnreachable}
	m.setInfo[setKey("prd", "product-development", "claims")] = &setSummary{
		members: []string{"claims.policy_claim", "claims.claim_detail"},
		added:   []string{"operations.policy_contract", "public.address"},
	}
	t.Logf("\n%s", m.View())

	m.focus = panelSnapshots
	t.Logf("\n%s", m.View())

	m.focus = panelSnapshots
	press(t, m, "a")
	t.Logf("\n%s", m.View())
}

var errUnreachable = probeError("dial tcp 10.0.0.1:5432: i/o timeout")

// probeError stands in for whatever a real connection failure returns.
type probeError string

func (e probeError) Error() string { return string(e) }
