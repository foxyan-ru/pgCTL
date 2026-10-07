package upgrade

import "time"

// Comparison is the before/after verdict for a single query.
type Comparison struct {
	Query         string  `json:"query"`
	BeforeMs      float64 `json:"beforeMs"`
	AfterMs       float64 `json:"afterMs"`
	DeltaPct      float64 `json:"deltaPct"` // (after-before)/before; positive = slower
	PlanChanged   bool    `json:"planChanged"`
	BeforePlan    string  `json:"beforePlan"`
	AfterPlan     string  `json:"afterPlan"`
	Regressed     bool    `json:"regressed"`
	MissingAfter  bool    `json:"missingAfter,omitempty"` // query errored or was absent in the "after" run
}

// ComparisonReport is the full triage output of `pgctl upgrade compare`.
type ComparisonReport struct {
	GeneratedAt   time.Time     `json:"generatedAt"`
	FailThreshold float64       `json:"failThreshold"`
	Comparisons   []Comparison  `json:"comparisons"`
	AnyRegression bool          `json:"anyRegression"`
}

// Compare diffs a "before" (source-version) report against an "after"
// (candidate-version) report.
//
// The regression check here is deliberately naive — a single-run percentage
// threshold — and that's a known, documented limitation, not an oversight:
// see docs/adr/0002-naive-threshold-vs-statistical-gate.md for why BenchGate
// (a separate, companion project) exists specifically to replace this with a
// variance-aware statistical test. pgctl's job is to prove the upgrade
// mechanics are sound; rigorous regression detection is a distinct problem.
func Compare(before, after *Report, failThreshold float64) *ComparisonReport {
	afterByQuery := make(map[string]QueryResult, len(after.Results))
	for _, r := range after.Results {
		afterByQuery[r.Query] = r
	}

	report := &ComparisonReport{
		GeneratedAt:   time.Now().UTC(),
		FailThreshold: failThreshold,
	}

	for _, b := range before.Results {
		a, ok := afterByQuery[b.Query]
		c := Comparison{
			Query:      b.Query,
			BeforeMs:   b.LatencyMs,
			BeforePlan: b.PlanNodeType,
		}

		if !ok || a.Error != "" || b.Error != "" {
			c.MissingAfter = true
			report.Comparisons = append(report.Comparisons, c)
			continue
		}

		c.AfterMs = a.LatencyMs
		c.AfterPlan = a.PlanNodeType
		c.PlanChanged = a.PlanNodeType != b.PlanNodeType

		if b.LatencyMs > 0 {
			c.DeltaPct = (a.LatencyMs - b.LatencyMs) / b.LatencyMs
		}

		c.Regressed = c.DeltaPct > failThreshold || c.PlanChanged
		if c.Regressed {
			report.AnyRegression = true
		}

		report.Comparisons = append(report.Comparisons, c)
	}

	return report
}
