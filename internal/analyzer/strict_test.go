package analyzer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tanrikuluozlem/burn/internal/collector"
	"github.com/tanrikuluozlem/burn/internal/pricing"
)

// strictMockProvider fails GetNodePricing with a strict pricing error for
// instance types listed in estimates, and prices everything else normally.
type strictMockProvider struct {
	estimates map[string]bool
	price     float64
}

func (m *strictMockProvider) GetHourlyPrice(context.Context, string, string, bool) (float64, error) {
	return m.price, nil
}
func (m *strictMockProvider) GetHourlyPriceForNode(ctx context.Context, n collector.NodeInfo) (float64, error) {
	return m.price, nil
}
func (m *strictMockProvider) GetNodePricing(_ context.Context, node collector.NodeInfo) (*pricing.NodePricing, error) {
	if m.estimates[node.InstanceType] {
		return nil, &pricing.EstimateError{
			Cloud:        "aws",
			Region:       node.Region,
			InstanceType: node.InstanceType,
			IsSpot:       node.IsSpot,
		}
	}
	return &pricing.NodePricing{HourlyTotal: m.price, Source: pricing.PriceSourceEmbedded}, nil
}
func (m *strictMockProvider) GetStoragePricePerGiBMonth(context.Context, string) float64 { return 0.1 }
func (m *strictMockProvider) GetLoadBalancerPricePerHour() float64                       { return 0.02 }
func (m *strictMockProvider) GetNetworkEgressPricePerGiB() float64                       { return 0.01 }
func (m *strictMockProvider) GetSpotDiscount(context.Context, string, string) pricing.SpotDiscount {
	return pricing.SpotDiscount{InterruptionRate: -1, Source: "unavailable"}
}

func strictNode(name, inst string) collector.NodeInfo {
	return collector.NodeInfo{
		Name:           name,
		InstanceType:   inst,
		Region:         "us-east-1",
		CloudProvider:  collector.CloudAWS,
		CPUAllocatable: 2000,
		MemAllocatable: 4 * 1024 * 1024 * 1024,
	}
}

func TestAnalyze_StrictAggregatesAllEstimateFailures(t *testing.T) {
	mp := &strictMockProvider{
		estimates: map[string]bool{"zz9.large": true, "zz9.xlarge": true},
		price:     0.20,
	}
	info := &collector.ClusterInfo{
		TotalNodes: 3,
		TotalPods:  3,
		Nodes: []collector.NodeInfo{
			strictNode("ok-1", "t3.medium"),
			strictNode("bad-1", "zz9.large"),
			strictNode("bad-2", "zz9.xlarge"),
		},
	}

	_, err := New(mp).Analyze(context.Background(), info)
	if err == nil {
		t.Fatal("expected aggregated strict pricing error")
	}

	var strictErr *StrictPricingError
	if !errors.As(err, &strictErr) {
		t.Fatalf("error type = %T, want *StrictPricingError", err)
	}
	if len(strictErr.Failures) != 2 {
		t.Fatalf("failures = %d, want 2", len(strictErr.Failures))
	}
	// The message must list both nodes' instance types together.
	msg := err.Error()
	if !strings.Contains(msg, "zz9.large") || !strings.Contains(msg, "zz9.xlarge") {
		t.Errorf("aggregated message missing both failures:\n%s", msg)
	}
	// And each failure must satisfy errors.Is(pricing.ErrPricingStrict).
	for _, f := range strictErr.Failures {
		if !errors.Is(f, pricing.ErrPricingStrict) {
			t.Errorf("failure %v does not wrap ErrPricingStrict", f)
		}
	}
}

func TestAnalyze_StrictSucceedsWhenAllPriced(t *testing.T) {
	mp := &strictMockProvider{estimates: map[string]bool{}, price: 0.20}
	info := &collector.ClusterInfo{
		TotalNodes: 2,
		TotalPods:  2,
		Nodes: []collector.NodeInfo{
			strictNode("ok-1", "t3.medium"),
			strictNode("ok-2", "t3.medium"),
		},
	}
	report, err := New(mp).Analyze(context.Background(), info)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(report.Nodes) != 2 {
		t.Errorf("nodes = %d, want 2", len(report.Nodes))
	}
	for _, n := range report.Nodes {
		if n.PriceSource != string(pricing.PriceSourceEmbedded) {
			t.Errorf("price source = %q, want embedded", n.PriceSource)
		}
	}
}
