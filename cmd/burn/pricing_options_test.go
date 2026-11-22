package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tanrikuluozlem/burn/internal/analyzer"
	"github.com/tanrikuluozlem/burn/internal/pricing"
)

// newTestProvider returns a provider that never dials a cloud API.
func newTestProvider(t *testing.T) *pricing.CloudPricingProvider {
	t.Helper()
	pp, err := pricing.NewCloudPricingProvider(context.Background())
	if err != nil {
		t.Fatalf("NewCloudPricingProvider() error = %v", err)
	}
	return pp
}

func TestPriceSourceNotices_QuietWithoutUserTable(t *testing.T) {
	nodes := []analyzer.NodeCost{
		{Name: "a", PriceSource: "embedded"},
		{Name: "b", PriceSource: "api"},
	}
	if got := priceSourceNotices(nodes); got != nil {
		t.Errorf("notices = %v, want nil when no user table and no fallback", got)
	}
}

func TestPriceSourceNotices_MentionsUserAndFallback(t *testing.T) {
	nodes := []analyzer.NodeCost{
		{Name: "a", PriceSource: "user"},
		{Name: "b", PriceSource: "embedded", RegionFallback: true},
	}
	notices := priceSourceNotices(nodes)
	joined := strings.Join(notices, "\n")
	if !strings.Contains(joined, "user price table") {
		t.Errorf("missing user table notice:\n%s", joined)
	}
	if !strings.Contains(joined, "default-region fallback") {
		t.Errorf("missing region fallback notice:\n%s", joined)
	}
}

func TestApplyPricingOptions_LoadsAndRejectsBadTable(t *testing.T) {
	dir := t.TempDir()

	// A valid table loads.
	good := filepath.Join(dir, "good.yaml")
	if err := os.WriteFile(good, []byte("regions:\n  us-east-1:\n    m5.large:\n      on_demand: 0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTable, oldStrict := priceTable, strictPricing
	defer func() { priceTable, strictPricing = oldTable, oldStrict }()
	priceTable, strictPricing = good, false

	pp := newTestProvider(t)
	if err := applyPricingOptions(pp); err != nil {
		t.Fatalf("applyPricingOptions() error = %v", err)
	}

	// A table with a duplicate key is rejected.
	bad := filepath.Join(dir, "bad.yaml")
	content := "regions:\n  us-east-1:\n    m5.large:\n      on_demand: 0.1\n    m5.large:\n      on_demand: 0.2\n"
	if err := os.WriteFile(bad, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	priceTable = bad
	err := applyPricingOptions(newTestProvider(t))
	if err == nil {
		t.Fatal("expected error for duplicate key table")
	}
	if !strings.Contains(err.Error(), "duplicate instance type") {
		t.Errorf("error = %q, want duplicate instance type", err.Error())
	}
}

func TestApplyPricingOptions_StrictFlag(t *testing.T) {
	oldTable, oldStrict := priceTable, strictPricing
	defer func() { priceTable, strictPricing = oldTable, oldStrict }()
	priceTable, strictPricing = "", true

	// applyPricingOptions must not error with no table but strict set.
	if err := applyPricingOptions(newTestProvider(t)); err != nil {
		t.Fatalf("applyPricingOptions() error = %v", err)
	}
}
