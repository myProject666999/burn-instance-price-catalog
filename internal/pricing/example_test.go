package pricing

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExamplePriceTableParses guards the shipped example against drift.
func TestExamplePriceTableParses(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "price-table.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("example table not present: %v", err)
	}
	table, err := LoadUserPriceTable(path)
	if err != nil {
		t.Fatalf("LoadUserPriceTable() error = %v", err)
	}

	// m5 family gets the 20% family discount, not the 10% global one.
	if p, ok := table.lookup("aws", "us-east-1", "m5.large", false); !ok || !almostEqual(p, 0.096*0.80) {
		t.Errorf("us-east-1 m5.large = %v, %v; want 0.0768", p, ok)
	}
	// c5 has no family rule, so the global 10% applies.
	if p, ok := table.lookup("aws", "us-east-1", "c5.large", false); !ok || !almostEqual(p, 0.085*0.90) {
		t.Errorf("us-east-1 c5.large = %v, %v; want 0.0765", p, ok)
	}
	// Declared spot price is not discounted.
	if p, ok := table.lookup("aws", "us-east-1", "c5.large", true); !ok || !almostEqual(p, 0.030) {
		t.Errorf("us-east-1 c5.large spot = %v, %v; want 0.030", p, ok)
	}
}
