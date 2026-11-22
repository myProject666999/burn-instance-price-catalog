package pricing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tanrikuluozlem/burn/internal/collector"
)

func mustParseTable(t *testing.T, src string) *UserPriceTable {
	t.Helper()
	table, err := ParseUserPriceTable([]byte(src))
	if err != nil {
		t.Fatalf("ParseUserPriceTable() error = %v", err)
	}
	return table
}

func TestUserPriceTableLookup(t *testing.T) {
	src := `
defaults:
  on_demand_discount: 0.10
families:
  m5:
    on_demand_discount: 0.20
regions:
  us-east-1:
    m5.large:
      on_demand: 0.100
      spot: 0.030
    c5.large:
      on_demand: 0.085
`
	table := mustParseTable(t, src)

	tests := []struct {
		name    string
		region  string
		inst    string
		isSpot  bool
		want    float64
		wantHit bool
	}{
		// Family discount (20%) beats global (10%); only one is applied.
		{"family on-demand", "us-east-1", "m5.large", false, 0.080, true},
		// No family rule for c5, so the global 10% applies.
		{"global on-demand", "us-east-1", "c5.large", false, 0.085 * 0.90, true},
		// Spot price uses the spot rule (none here) -> no discount.
		{"spot no discount", "us-east-1", "m5.large", true, 0.030, true},
		// Unknown region misses.
		{"unknown region", "eu-west-1", "m5.large", false, 0, false},
		// Unknown instance misses.
		{"unknown instance", "us-east-1", "r5.large", false, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := table.lookup("aws", tt.region, tt.inst, tt.isSpot)
			if ok != tt.wantHit {
				t.Fatalf("hit = %v, want %v", ok, tt.wantHit)
			}
			if ok && !almostEqual(got, tt.want) {
				t.Errorf("lookup = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserPriceTableSpotDiscountDoesNotStackOnDemand(t *testing.T) {
	src := `
defaults:
  on_demand_discount: 0.30
  spot_discount: 0.05
regions:
  us-east-1:
    m5.large:
      on_demand: 0.100
      spot: 0.050
`
	table := mustParseTable(t, src)

	od, _ := table.lookup("aws", "us-east-1", "m5.large", false)
	spot, _ := table.lookup("aws", "us-east-1", "m5.large", true)

	if !almostEqual(od, 0.070) {
		t.Errorf("on-demand = %v, want 0.070 (30%% off only)", od)
	}
	if !almostEqual(spot, 0.0475) {
		t.Errorf("spot = %v, want 0.0475 (spot 5%% off, no on-demand)", spot)
	}
	// Explicitly assert the spot price is not on-demand discounted, nor
	// multiplied by the fallback multiplier.
	if almostEqual(spot, 0.050*(1-0.30)*(1-0.05)) {
		t.Errorf("spot compounded the two discounts: %v", spot)
	}
}

func TestUserPriceTableSpotMissingMisses(t *testing.T) {
	src := `
regions:
  us-east-1:
    m5.large:
      on_demand: 0.100
`
	table := mustParseTable(t, src)
	if _, ok := table.lookup("aws", "us-east-1", "m5.large", true); ok {
		t.Error("spot lookup should miss when no spot price is declared")
	}
	if p, ok := table.lookup("aws", "us-east-1", "m5.large", false); !ok || !almostEqual(p, 0.1) {
		t.Errorf("on-demand lookup = %v, %v", p, ok)
	}
}

func TestUserPriceTableValidation(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantSub string
	}{
		{
			name: "duplicate region",
			src: `
regions:
  us-east-1:
    m5.large:
      on_demand: 0.1
  us-east-1:
    m5.large:
      on_demand: 0.2
`,
			wantSub: "duplicate region",
		},
		{
			name: "duplicate instance type",
			src: `
regions:
  us-east-1:
    m5.large:
      on_demand: 0.1
    m5.large:
      on_demand: 0.2
`,
			wantSub: "duplicate instance type",
		},
		{
			name: "duplicate price field",
			src: `
regions:
  us-east-1:
    m5.large:
      on_demand: 0.1
      on_demand: 0.2
`,
			wantSub: "duplicate price field",
		},
		{
			name: "duplicate top-level",
			src: `
regions: {}
regions: {}
`,
			wantSub: "duplicate top-level key",
		},
		{
			name: "negative price",
			src: `
regions:
  us-east-1:
    m5.large:
      on_demand: -0.1
`,
			wantSub: "must not be negative",
		},
		{
			name: "missing on_demand",
			src: `
regions:
  us-east-1:
    m5.large:
      spot: 0.03
`,
			wantSub: "missing required on_demand",
		},
		{
			name: "discount out of range high",
			src: `
defaults:
  on_demand_discount: 1.0
`,
			wantSub: "must be in [0, 1)",
		},
		{
			name: "discount out of range low",
			src: `
defaults:
  spot_discount: -0.1
`,
			wantSub: "must be in [0, 1)",
		},
		{
			name: "unknown top-level key",
			src: `
regions: {}
bogus: 1
`,
			wantSub: "unknown top-level key",
		},
		{
			name: "unknown price field",
			src: `
regions:
  us-east-1:
    m5.large:
      on_demand: 0.1
      ondemand: 0.2
`,
			wantSub: "unknown price field",
		},
		{
			name: "non-numeric price",
			src: `
regions:
  us-east-1:
    m5.large:
      on_demand: "cheap"
`,
			wantSub: "expected a number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseUserPriceTable([]byte(tt.src))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestUserPriceTableDuplicateKeyReportsPosition(t *testing.T) {
	src := "regions:\n  us-east-1:\n    m5.large:\n      on_demand: 0.1\n    m5.large:\n      on_demand: 0.2\n"
	_, err := ParseUserPriceTable([]byte(src))
	if err == nil {
		t.Fatal("expected duplicate-key error")
	}
	msg := err.Error()
	// The repeated key is on line 5, the first on line 3.
	if !strings.Contains(msg, "line 5:5") {
		t.Errorf("error does not point at repeated key line 5:5: %q", msg)
	}
	if !strings.Contains(msg, "first declared at line 3:5") {
		t.Errorf("error does not point at first declaration line 3:5: %q", msg)
	}
}

func TestLoadUserPriceTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.yaml")
	content := `
regions:
  us-east-1:
    m5.large:
      on_demand: 0.096
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	table, err := LoadUserPriceTable(path)
	if err != nil {
		t.Fatalf("LoadUserPriceTable() error = %v", err)
	}
	if p, ok := table.lookup("aws", "us-east-1", "m5.large", false); !ok || !almostEqual(p, 0.096) {
		t.Errorf("lookup = %v, %v", p, ok)
	}
}

// --- Integration with the provider chain ---

// providerWithUserTable builds a CloudPricingProvider whose cloud is AWS and
// whose user table is supplied, without touching any network.
func providerWithAWSUserTable(t *testing.T, src string) *CloudPricingProvider {
	t.Helper()
	cp := &CloudPricingProvider{
		azure:    NewAzureProvider(),
		fallback: NewStaticProvider(),
	}
	cp.awsOnce.Do(func() {}) // mark AWS init as done so initAWS does not dial out
	cp.detectedCloud = collector.CloudAWS
	cp.SetUserPriceTable(mustParseTable(t, src))
	return cp
}

func awsNode(inst, region string, spot bool) collector.NodeInfo {
	return collector.NodeInfo{
		Name:           "node-" + inst,
		InstanceType:   inst,
		Region:         region,
		CloudProvider:  collector.CloudAWS,
		CPUAllocatable: 2000,
		MemAllocatable: 4 * 1024 * 1024 * 1024,
		IsSpot:         spot,
	}
}

func TestProviderPriority_UserWins(t *testing.T) {
	// t3.micro exists in the embedded catalog (0.0104 in us-east-1) and would
	// otherwise be used; the user table must win.
	cp := providerWithAWSUserTable(t, `
regions:
  us-east-1:
    t3.micro:
      on_demand: 0.5
`)
	np, err := cp.GetNodePricing(context.Background(), awsNode("t3.micro", "us-east-1", false))
	if err != nil {
		t.Fatalf("GetNodePricing() error = %v", err)
	}
	if np.HourlyTotal != 0.5 {
		t.Errorf("hourly = %v, want 0.5 (user)", np.HourlyTotal)
	}
	if np.Source != PriceSourceUser {
		t.Errorf("source = %q, want %q", np.Source, PriceSourceUser)
	}
}

func TestProviderPriority_PartialUserTableFallsThrough(t *testing.T) {
	// Table declares only t3.micro; t3.small must still come from embedded.
	cp := providerWithAWSUserTable(t, `
regions:
  us-east-1:
    t3.micro:
      on_demand: 0.5
`)
	np, err := cp.GetNodePricing(context.Background(), awsNode("t3.small", "us-east-1", false))
	if err != nil {
		t.Fatalf("GetNodePricing() error = %v", err)
	}
	if np.Source != PriceSourceEmbedded {
		t.Errorf("source = %q, want embedded", np.Source)
	}
	embedded, _ := GetEmbeddedPrice("aws", "us-east-1", "t3.small")
	if np.HourlyTotal != embedded {
		t.Errorf("hourly = %v, want embedded %v", np.HourlyTotal, embedded)
	}
}

func TestProviderPriority_EmbeddedBeforeEstimate(t *testing.T) {
	cp := providerWithAWSUserTable(t, `{}`)
	np, err := cp.GetNodePricing(context.Background(), awsNode("t3.micro", "us-east-1", false))
	if err != nil {
		t.Fatalf("GetNodePricing() error = %v", err)
	}
	if np.Source != PriceSourceEmbedded {
		t.Errorf("source = %q, want embedded", np.Source)
	}
}

func TestProviderPriority_EstimateLastResort(t *testing.T) {
	cp := providerWithAWSUserTable(t, `{}`)
	np, err := cp.GetNodePricing(context.Background(), awsNode("zz9.large", "us-east-1", false))
	if err != nil {
		t.Fatalf("GetNodePricing() error = %v", err)
	}
	if np.Source != PriceSourceEstimated {
		t.Errorf("source = %q, want estimated", np.Source)
	}
	if !almostEqual(np.HourlyTotal, 0.10) {
		t.Errorf("hourly = %v, want 0.10 estimate", np.HourlyTotal)
	}
}

func TestProviderPriority_RegionFallbackMarked(t *testing.T) {
	// ap-south-1 has no embedded entry for t3.micro except via us-east-1.
	cp := providerWithAWSUserTable(t, `{}`)
	np, err := cp.GetNodePricing(context.Background(), awsNode("t3.micro", "unknown-region-9", false))
	if err != nil {
		t.Fatalf("GetNodePricing() error = %v", err)
	}
	if np.Source != PriceSourceEmbedded {
		t.Errorf("source = %q, want embedded", np.Source)
	}
	if !np.RegionFallback {
		t.Error("RegionFallback should be true when the default region is substituted")
	}
	// And the price must equal us-east-1's value.
	if !almostEqual(np.HourlyTotal, 0.0104) {
		t.Errorf("hourly = %v, want us-east-1 t3.micro 0.0104", np.HourlyTotal)
	}
}

func TestProviderPriority_NoRegionFallbackWhenPresent(t *testing.T) {
	cp := providerWithAWSUserTable(t, `{}`)
	np, err := cp.GetNodePricing(context.Background(), awsNode("t3.micro", "us-east-1", false))
	if err != nil {
		t.Fatalf("GetNodePricing() error = %v", err)
	}
	if np.RegionFallback {
		t.Error("RegionFallback should be false for a region present in the catalog")
	}
}

func TestProviderSpot_UserSpotPriceNotCompounded(t *testing.T) {
	cp := providerWithAWSUserTable(t, `
regions:
  us-east-1:
    t3.micro:
      on_demand: 0.100
      spot: 0.030
`)
	price, err := cp.GetHourlyPrice(context.Background(), "t3.micro", "us-east-1", true)
	if err != nil {
		t.Fatalf("GetHourlyPrice(spot) error = %v", err)
	}
	if !almostEqual(price, 0.030) {
		t.Errorf("spot = %v, want 0.030 (user spot, no fallback multiplier)", price)
	}
}

func TestStrictMode_AggregatesIfNodeEstimated(t *testing.T) {
	cp := providerWithAWSUserTable(t, `{}`)
	cp.SetStrict(true)

	_, err := cp.GetNodePricing(context.Background(), awsNode("zz9.large", "us-east-1", false))
	if err == nil {
		t.Fatal("expected strict error for estimate-only instance")
	}
	if !strings.Contains(err.Error(), "strict pricing") {
		t.Errorf("error = %q, want strict pricing mention", err.Error())
	}
	var estErr *EstimateError
	if !asError(err, &estErr) {
		t.Errorf("error %T is not *EstimateError", err)
	}
}

func TestStrictMode_EmbeddedStillWorks(t *testing.T) {
	cp := providerWithAWSUserTable(t, `{}`)
	cp.SetStrict(true)
	np, err := cp.GetNodePricing(context.Background(), awsNode("t3.micro", "us-east-1", false))
	if err != nil {
		t.Fatalf("embedded price should satisfy strict mode, got %v", err)
	}
	if np.Source != PriceSourceEmbedded {
		t.Errorf("source = %q, want embedded", np.Source)
	}
}

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

// asError is a tiny errors.As wrapper kept local to avoid importing errors in
// every test in this file.
func asError(err error, target any) bool {
	switch tgt := target.(type) {
	case **EstimateError:
		for err != nil {
			if e, ok := err.(*EstimateError); ok {
				*tgt = e
				return true
			}
			u, ok := err.(interface{ Unwrap() error })
			if !ok {
				return false
			}
			err = u.Unwrap()
		}
	}
	return false
}
