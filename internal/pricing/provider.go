package pricing

import (
	"context"

	"github.com/tanrikuluozlem/burn/internal/collector"
)

// PriceSource identifies which layer supplied a node's hourly price. The zero
// value is intentionally "unknown" so that unchanged call sites keep working.
type PriceSource string

const (
	// PriceSourceUser is an explicit user price table entry.
	PriceSourceUser PriceSource = "user"
	// PriceSourceAPI is a live cloud pricing API.
	PriceSourceAPI PriceSource = "api"
	// PriceSourceEmbedded is the embedded YAML catalog.
	PriceSourceEmbedded PriceSource = "embedded"
	// PriceSourceEstimated is the instance-suffix estimate fallback.
	PriceSourceEstimated PriceSource = "estimated"
	// PriceSourceCustom is on-prem per-core/per-GiB custom pricing.
	PriceSourceCustom PriceSource = "custom"
)

type NodePricing struct {
	HourlyTotal    float64
	CPUCostPerCore float64
	RAMCostPerGiB  float64
	GPUCostPerUnit float64
	// Source records where HourlyTotal came from. Empty for providers that do
	// not report a source.
	Source PriceSource
	// RegionFallback is true when the price came from a different region than
	// the one requested (e.g. an embedded catalog served us-east-1 for an
	// unknown region). The request still succeeds, but callers can surface it.
	RegionFallback bool
}

type CustomPricing struct {
	CPUCostPerCoreHr     float64
	RAMCostPerGiBHr      float64
	GPUCostPerHr         float64
	StoragePricePerGiBMo float64
}

type SpotDiscount struct {
	Discount         float64 // 0.0-1.0
	InterruptionRate int     // 0-4 scale, -1 = unknown
	Source           string  // "api", "advisor", "default"
}

type Provider interface {
	GetHourlyPrice(ctx context.Context, instanceType, region string, isSpot bool) (float64, error)
	GetHourlyPriceForNode(ctx context.Context, node collector.NodeInfo) (float64, error)
	GetNodePricing(ctx context.Context, node collector.NodeInfo) (*NodePricing, error)
	GetStoragePricePerGiBMonth(ctx context.Context, storageClass string) float64
	GetLoadBalancerPricePerHour() float64
	GetNetworkEgressPricePerGiB() float64
	GetSpotDiscount(ctx context.Context, instanceType, region string) SpotDiscount
}
