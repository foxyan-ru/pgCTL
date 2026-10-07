package upgrade

import "testing"

func TestCompare_FlagsLatencyRegression(t *testing.T) {
	before := &Report{Results: []QueryResult{
		{Query: "SELECT 1", LatencyMs: 4.0, PlanNodeType: "Result"},
	}}
	after := &Report{Results: []QueryResult{
		{Query: "SELECT 1", LatencyMs: 22.0, PlanNodeType: "Result"},
	}}

	got := Compare(before, after, 0.20)

	if !got.AnyRegression {
		t.Fatalf("expected a regression to be flagged (4ms -> 22ms is a 450%% increase)")
	}
	if len(got.Comparisons) != 1 {
		t.Fatalf("expected 1 comparison, got %d", len(got.Comparisons))
	}
	c := got.Comparisons[0]
	if !c.Regressed {
		t.Errorf("expected Comparisons[0].Regressed = true")
	}
	if c.PlanChanged {
		t.Errorf("plan node type didn't change, PlanChanged should be false")
	}
}

func TestCompare_FlagsPlanShapeChangeEvenWithoutLatencyRegression(t *testing.T) {
	before := &Report{Results: []QueryResult{
		{Query: "SELECT * FROM widgets WHERE id = 1", LatencyMs: 1.0, PlanNodeType: "Index Scan"},
	}}
	after := &Report{Results: []QueryResult{
		{Query: "SELECT * FROM widgets WHERE id = 1", LatencyMs: 1.1, PlanNodeType: "Seq Scan"},
	}}

	got := Compare(before, after, 0.20)

	if !got.AnyRegression {
		t.Fatalf("a plan shape change (Index Scan -> Seq Scan) should be flagged even at near-identical latency")
	}
}

func TestCompare_WithinThresholdIsNotRegressed(t *testing.T) {
	before := &Report{Results: []QueryResult{
		{Query: "SELECT 1", LatencyMs: 10.0, PlanNodeType: "Result"},
	}}
	after := &Report{Results: []QueryResult{
		{Query: "SELECT 1", LatencyMs: 11.0, PlanNodeType: "Result"},
	}}

	got := Compare(before, after, 0.20)

	if got.AnyRegression {
		t.Fatalf("a 10%% latency increase should not trip a 20%% threshold")
	}
}

func TestCompare_MissingAfterIsFlaggedNotSkipped(t *testing.T) {
	before := &Report{Results: []QueryResult{
		{Query: "SELECT 1", LatencyMs: 1.0, PlanNodeType: "Result"},
		{Query: "SELECT 2", LatencyMs: 1.0, PlanNodeType: "Result"},
	}}
	after := &Report{Results: []QueryResult{
		{Query: "SELECT 1", LatencyMs: 1.0, PlanNodeType: "Result"},
		// "SELECT 2" errored out during replay against the candidate.
		{Query: "SELECT 2", LatencyMs: 0, Error: "syntax error"},
	}}

	got := Compare(before, after, 0.20)

	if len(got.Comparisons) != 2 {
		t.Fatalf("expected both queries in the comparison, got %d", len(got.Comparisons))
	}
	if !got.Comparisons[1].MissingAfter {
		t.Errorf("expected SELECT 2 to be flagged MissingAfter since it errored during replay")
	}
}
