package pg

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// fk is a test helper: the graph only cares about child and parent.
func fk(name, child, parent string) FK {
	return FK{Name: name, Child: child, Parent: parent}
}

// A slice of the real MBPNetwork shape: contracts hang off accounts, cost
// detail and assets hang off contracts, claims hang off contracts.
func sample() *Graph {
	return NewGraph(
		[]string{
			"operations.account",
			"operations.policy_contract",
			"operations.policy_contract_cost_detail",
			"operations.policy_contract_asset",
			"claims.policy_claim",
			"claims.claim_detail",
			"public.address",
		},
		[]FK{
			fk("fk_contract_account", "operations.policy_contract", "operations.account"),
			fk("fk_cost_contract", "operations.policy_contract_cost_detail", "operations.policy_contract"),
			fk("fk_asset_contract", "operations.policy_contract_asset", "operations.policy_contract"),
			fk("fk_claim_contract", "claims.policy_claim", "operations.policy_contract"),
			fk("fk_detail_claim", "claims.claim_detail", "claims.policy_claim"),
			fk("fk_account_address", "operations.account", "public.address"),
		},
	)
}

func TestClosurePullsInTransitiveParents(t *testing.T) {
	g := sample()
	got := g.Closure([]string{"claims.claim_detail"})
	want := []string{
		"claims.claim_detail",
		"claims.policy_claim",
		"operations.account",
		"operations.policy_contract",
		"public.address",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Closure = %v, want %v", got, want)
	}
}

func TestMissingParentsNamesWhatWouldBreakTheRestore(t *testing.T) {
	g := sample()

	// The failure this tool exists to prevent: taking the claims tables
	// without the contracts they reference.
	got := g.MissingParents([]string{"claims.policy_claim", "claims.claim_detail"})
	want := []string{"operations.account", "operations.policy_contract", "public.address"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MissingParents = %v, want %v", got, want)
	}

	// A closed selection reports nothing.
	closed := g.Closure([]string{"claims.claim_detail"})
	if missing := g.MissingParents(closed); len(missing) != 0 {
		t.Errorf("a closure reported missing parents: %v", missing)
	}
}

func TestLoadOrderPutsParentsFirst(t *testing.T) {
	g := sample()
	plan := g.LoadOrder(g.Closure([]string{"claims.claim_detail"}))
	if len(plan.Cycles) != 0 {
		t.Fatalf("unexpected cycles: %v", plan.Cycles)
	}

	position := map[string]int{}
	for i, layer := range plan.Layers {
		for _, table := range layer {
			position[table] = i
		}
	}
	for _, pair := range [][2]string{
		{"public.address", "operations.account"},
		{"operations.account", "operations.policy_contract"},
		{"operations.policy_contract", "claims.policy_claim"},
		{"claims.policy_claim", "claims.claim_detail"},
	} {
		if position[pair[0]] >= position[pair[1]] {
			t.Errorf("%s (layer %d) must load before %s (layer %d)",
				pair[0], position[pair[0]], pair[1], position[pair[1]])
		}
	}
}

func TestLoadOrderIgnoresParentsOutsideTheSelection(t *testing.T) {
	g := sample()
	// Asked for the claims tables alone, the order is still an order — it just
	// cannot account for contracts, which is MissingParents' job to say.
	plan := g.LoadOrder([]string{"claims.claim_detail", "claims.policy_claim"})
	want := []Layer{{"claims.policy_claim"}, {"claims.claim_detail"}}
	if !reflect.DeepEqual(plan.Layers, want) {
		t.Errorf("Layers = %v, want %v", plan.Layers, want)
	}
}

func TestTruncateOrderIsLoadOrderReversed(t *testing.T) {
	g := sample()
	selection := g.Closure([]string{"claims.claim_detail"})
	load := g.LoadOrder(selection).Flat()
	truncate := g.TruncateOrder(selection).Flat()

	if len(load) != len(truncate) {
		t.Fatalf("load has %d tables, truncate %d", len(load), len(truncate))
	}
	if truncate[0] != "claims.claim_detail" {
		t.Errorf("truncate starts at %q, want the leaf table", truncate[0])
	}
	if last := truncate[len(truncate)-1]; last != "public.address" {
		t.Errorf("truncate ends at %q, want the root table", last)
	}
}

func TestSelfReferenceIsNotACycle(t *testing.T) {
	// A parent_id hierarchy constrains row order inside one load, not the
	// order tables load in. Treating it as a cycle would flag most trees.
	g := NewGraph(
		[]string{"operations.account"},
		[]FK{fk("fk_account_parent", "operations.account", "operations.account")},
	)
	plan := g.LoadOrder([]string{"operations.account"})
	if len(plan.Cycles) != 0 {
		t.Errorf("self-reference reported as a cycle: %v", plan.Cycles)
	}
	if len(plan.Layers) != 1 {
		t.Errorf("Layers = %v, want one layer", plan.Layers)
	}
}

func TestMutualReferenceIsReportedAsACycle(t *testing.T) {
	g := NewGraph(
		[]string{"a.one", "a.two", "a.free"},
		[]FK{
			fk("fk_one_two", "a.one", "a.two"),
			fk("fk_two_one", "a.two", "a.one"),
		},
	)
	plan := g.LoadOrder([]string{"a.one", "a.two", "a.free"})

	// The ring is still placed — a set-level apply drops its foreign keys —
	// and reported, so nothing in the selection goes missing from the plan.
	if len(plan.Layers) != 1 {
		t.Errorf("Layers = %v, want one layer holding everything", plan.Layers)
	}
	if got := plan.Flat(); !reflect.DeepEqual(got, []string{"a.free", "a.one", "a.two"}) {
		t.Errorf("Flat = %v, want every selected table", got)
	}
	if len(plan.Cycles) != 1 {
		t.Fatalf("Cycles = %v, want one group", plan.Cycles)
	}
	if !reflect.DeepEqual([]string(plan.Cycles[0]), []string{"a.one", "a.two"}) {
		t.Errorf("cycle = %v, want both tables", plan.Cycles[0])
	}
	if !strings.Contains(plan.String(), "cycle: a.one, a.two") {
		t.Errorf("String() hides the cycle:\n%s", plan.String())
	}
}

func TestFKsInboundFindsWhatBlocksATruncate(t *testing.T) {
	g := sample()
	// Reloading contracts alone: claims still reference the rows going away.
	inbound := g.FKsInbound([]string{"operations.policy_contract"})
	want := []string{"fk_asset_contract", "fk_claim_contract", "fk_cost_contract"}
	if got := names(inbound); !reflect.DeepEqual(got, want) {
		t.Errorf("FKsInbound = %v, want %v", got, want)
	}
}

func TestFKsWithinFindsWhatMustBeDropped(t *testing.T) {
	g := sample()
	within := g.FKsWithin([]string{"claims.policy_claim", "claims.claim_detail"})
	got := names(within)
	want := []string{"fk_claim_contract", "fk_detail_claim"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FKsWithin = %v, want %v", got, want)
	}
}

func names(fks []FK) []string {
	out := make([]string, 0, len(fks))
	for _, f := range fks {
		out = append(out, f.Name)
	}
	return out
}

// A ring must hold up only what is genuinely behind it. The MBPNetwork schema
// has operations.cancellation and operations.policy_contract referencing each
// other, with a long tail of tables hanging off the contract; an ordering that
// gave up on the ring would drag that whole tail into the unorderable pile.
func TestCycleDoesNotSwallowDownstreamTables(t *testing.T) {
	g := NewGraph(
		[]string{"o.contract", "o.cancellation", "o.cost_detail", "o.account", "c.claim"},
		[]FK{
			fk("fk_contract_cancellation", "o.contract", "o.cancellation"),
			fk("fk_cancellation_contract", "o.cancellation", "o.contract"),
			fk("fk_contract_account", "o.contract", "o.account"),
			fk("fk_cost_contract", "o.cost_detail", "o.contract"),
			fk("fk_claim_contract", "c.claim", "o.contract"),
		},
	)
	plan := g.LoadOrder(g.Tables())

	if len(plan.Cycles) != 1 || !reflect.DeepEqual([]string(plan.Cycles[0]), []string{"o.cancellation", "o.contract"}) {
		t.Errorf("Cycles = %v, want just the mutually-referencing pair", plan.Cycles)
	}
	want := []Layer{
		{"o.account"},
		{"o.cancellation", "o.contract"},
		{"c.claim", "o.cost_detail"},
	}
	if !reflect.DeepEqual(plan.Layers, want) {
		t.Errorf("Layers = %v, want %v", plan.Layers, want)
	}
}

func TestLoadOrderCoversEverySelectedTable(t *testing.T) {
	g := sample()
	selection := g.Tables()
	got := g.LoadOrder(selection).Flat()
	sort.Strings(got)
	if !reflect.DeepEqual(got, selection) {
		t.Errorf("Flat = %v, want every table %v", got, selection)
	}
}
