package pricing

import (
	"strings"
	"testing"
)

// TestEmbeddedCatalogIntegrity asserts the shipped catalogs contain no
// duplicate keys or negative prices, so a corrupted update fails CI rather than
// silently serving the wrong price.
func TestEmbeddedCatalogIntegrity(t *testing.T) {
	if err := validateEmbeddedData(); err != nil {
		t.Fatalf("embedded catalog is invalid: %v", err)
	}
}

func TestValidatePriceYAML_DetectsDuplicates(t *testing.T) {
	src := []byte("us-east-1:\n  t3.micro: 0.01\n  t3.micro: 0.02\n")
	err := validatePriceYAML(src)
	if err == nil {
		t.Fatal("expected duplicate-key error")
	}
	if !strings.Contains(err.Error(), "duplicate instance type") {
		t.Errorf("error = %q, want duplicate instance type", err.Error())
	}
}

func TestValidatePriceYAML_DetectsNegative(t *testing.T) {
	src := []byte("us-east-1:\n  t3.micro: -0.01\n")
	err := validatePriceYAML(src)
	if err == nil || !strings.Contains(err.Error(), "negative price") {
		t.Errorf("error = %v, want negative price", err)
	}
}

func TestLookupEmbeddedPrice_RegionFallbackFlag(t *testing.T) {
	price, fallback, err := LookupEmbeddedPrice("aws", "not-a-region", "t3.micro")
	if err != nil {
		t.Fatalf("LookupEmbeddedPrice() error = %v", err)
	}
	if !fallback {
		t.Error("fallback = false, want true for unknown region")
	}
	if price != 0.0104 {
		t.Errorf("price = %v, want us-east-1 t3.micro 0.0104", price)
	}

	_, fallback, err = LookupEmbeddedPrice("aws", "us-east-1", "t3.micro")
	if err != nil {
		t.Fatal(err)
	}
	if fallback {
		t.Error("fallback = true for a present region")
	}
}
