package pricing

import "testing"

func TestBlend_SixNodesFourCovered(t *testing.T) {
	// 6 nodes, coverage 4/6: 4 pay commit price, 2 pay on-demand.
	terms := CommitmentTerms{
		OnDemandHourly: 0.20,
		CommitHourly:   0.12,
		Coverage:       4.0 / 6.0,
	}
	res, err := Blend(6, terms)
	if err != nil {
		t.Fatalf("Blend() error = %v", err)
	}
	// (4*0.12 + 2*0.20) / 6 = (0.48 + 0.40)/6 = 0.88/6 = 0.146666...
	want := (4*0.12 + 2*0.20) / 6
	if !almostEqual(res.BlendHourly, want) {
		t.Errorf("blend = %v, want %v", res.BlendHourly, want)
	}
	if !almostEqual(res.CoveredCount, 4) {
		t.Errorf("covered count = %v, want 4", res.CoveredCount)
	}
	if !almostEqual(res.OnDemandCount, 2) {
		t.Errorf("on-demand count = %v, want 2", res.OnDemandCount)
	}
}

func TestBlend_CoverageEndpoints(t *testing.T) {
	base := CommitmentTerms{OnDemandHourly: 0.20, CommitHourly: 0.10}

	// Coverage 0 -> pure on-demand.
	base.Coverage = 0
	res, err := Blend(4, base)
	if err != nil {
		t.Fatal(err)
	}
	if !almostEqual(res.BlendHourly, 0.20) {
		t.Errorf("coverage 0 blend = %v, want 0.20", res.BlendHourly)
	}

	// Coverage 0.5 -> halfway.
	base.Coverage = 0.5
	res, _ = Blend(4, base)
	if !almostEqual(res.BlendHourly, 0.15) {
		t.Errorf("coverage 0.5 blend = %v, want 0.15", res.BlendHourly)
	}

	// Coverage 1 -> pure commit.
	base.Coverage = 1
	res, _ = Blend(4, base)
	if !almostEqual(res.BlendHourly, 0.10) {
		t.Errorf("coverage 1 blend = %v, want 0.10", res.BlendHourly)
	}
}

func TestBlend_InvalidRanges(t *testing.T) {
	tests := []struct {
		name  string
		nodes int
		terms CommitmentTerms
	}{
		{"coverage below 0", 4, CommitmentTerms{OnDemandHourly: 0.2, CommitHourly: 0.1, Coverage: -0.1}},
		{"coverage above 1", 4, CommitmentTerms{OnDemandHourly: 0.2, CommitHourly: 0.1, Coverage: 1.5}},
		{"negative price", 4, CommitmentTerms{OnDemandHourly: -0.2, CommitHourly: 0.1, Coverage: 0.5}},
		{"commit above on-demand", 4, CommitmentTerms{OnDemandHourly: 0.1, CommitHourly: 0.2, Coverage: 0.5}},
		{"zero nodes", 0, CommitmentTerms{OnDemandHourly: 0.2, CommitHourly: 0.1, Coverage: 0.5}},
		{"negative nodes", -1, CommitmentTerms{OnDemandHourly: 0.2, CommitHourly: 0.1, Coverage: 0.5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Blend(tt.nodes, tt.terms); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestBlendDiscount(t *testing.T) {
	terms := CommitmentTerms{OnDemandHourly: 0.20, CommitHourly: 0.12, Coverage: 0.5}
	res, err := Blend(2, terms)
	if err != nil {
		t.Fatal(err)
	}
	// blend = (0.12 + 0.20)/2 = 0.16; discount vs 0.20 = 20%.
	if d := res.BlendDiscount(0.20); !almostEqual(d, 0.20) {
		t.Errorf("blend discount = %v, want 0.20", d)
	}
}
