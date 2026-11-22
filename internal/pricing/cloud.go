package pricing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tanrikuluozlem/burn/internal/collector"
)

type CloudPricingProvider struct {
	aws           *AWSProvider
	awsOnce       sync.Once
	azure         *AzureProvider
	fallback      *StaticProvider
	customPricing *CustomPricing
	userTable     *UserPriceTable
	strict        bool
	cloudOnce     sync.Once
	detectedCloud collector.CloudProvider
}

func NewCloudPricingProvider(ctx context.Context) (*CloudPricingProvider, error) {
	return &CloudPricingProvider{
		azure:    NewAzureProvider(),
		fallback: NewStaticProvider(),
	}, nil
}

func (p *CloudPricingProvider) initAWS(ctx context.Context) {
	p.awsOnce.Do(func() {
		prov, err := NewAWSProvider(ctx)
		if err != nil {
			slog.Debug("aws pricing api unavailable", "err", err)
			return
		}
		p.aws = prov
	})
}

func (p *CloudPricingProvider) SetCustomPricing(cp *CustomPricing) {
	p.customPricing = cp
}

// SetUserPriceTable configures an explicit user price table. It takes
// precedence over every online, embedded and estimated source, but only for the
// instance types it declares; everything else continues down the normal chain.
func (p *CloudPricingProvider) SetUserPriceTable(t *UserPriceTable) {
	p.userTable = t
}

// SetStrict makes pricing refuse estimates. In strict mode a lookup that can
// only be satisfied by the instance-suffix estimator returns ErrPricingStrict
// instead of a made-up number. Callers that price many nodes should aggregate
// these errors and fail once, rather than logging per node.
func (p *CloudPricingProvider) SetStrict(strict bool) {
	p.strict = strict
}

// ErrPricingStrict is returned (wrapped) when a price can only be estimated and
// strict mode is enabled.
var ErrPricingStrict = errors.New("strict pricing: no exact price available")

// ErrPricingStrict wraps an estimate-only result for an instance type.
type EstimateError struct {
	Cloud        string
	Region       string
	InstanceType string
	IsSpot       bool
}

func (e *EstimateError) Error() string {
	kind := "on-demand"
	if e.IsSpot {
		kind = "spot"
	}
	return fmt.Sprintf("%s: %s/%s/%s %s price can only be estimated", ErrPricingStrict, e.Cloud, e.Region, e.InstanceType, kind)
}

func (e *EstimateError) Unwrap() error { return ErrPricingStrict }

func (p *CloudPricingProvider) detectCloud(node collector.NodeInfo) {
	p.cloudOnce.Do(func() {
		if node.CloudProvider != collector.CloudUnknown {
			p.detectedCloud = node.CloudProvider
		}
		if node.Region != "" {
			p.fallback.SetRegion(node.Region)
		}
	})
}

func (p *CloudPricingProvider) GetHourlyPriceForNode(ctx context.Context, node collector.NodeInfo) (float64, error) {
	p.detectCloud(node)
	return p.hourlyPrice(ctx, node.InstanceType, node.Region, node.Zone, node.IsSpot)
}

func (p *CloudPricingProvider) GetHourlyPrice(ctx context.Context, instanceType, region string, isSpot bool) (float64, error) {
	return p.hourlyPrice(ctx, instanceType, region, "", isSpot)
}

// cloudName maps the detected cloud to a catalog key.
func (p *CloudPricingProvider) cloudName() string {
	switch p.detectedCloud {
	case collector.CloudAWS:
		return "aws"
	case collector.CloudAzure:
		return "azure"
	case collector.CloudGCP:
		return "gcp"
	default:
		return ""
	}
}

func (p *CloudPricingProvider) hourlyPrice(ctx context.Context, instanceType, region, zone string, isSpot bool) (float64, error) {
	price, _, _, err := p.resolveHourly(ctx, instanceType, region, zone, isSpot)
	return price, err
}

// resolveHourly returns the hourly price, the layer that supplied it, and
// whether a default region was substituted for the requested one.
//
// Priority (first hit wins): user table, live cloud API, embedded catalog,
// instance-suffix estimate. The user table only covers the instance types it
// declares; everything else falls through unchanged.
func (p *CloudPricingProvider) resolveHourly(ctx context.Context, instanceType, region, zone string, isSpot bool) (float64, PriceSource, bool, error) {
	cloudName := p.cloudName()

	if price, ok := p.userTable.lookup(cloudName, region, instanceType, isSpot); ok {
		return price, PriceSourceUser, false, nil
	}

	// Drill-down into a single source. Each layer reports whether it produced a
	// price; errors are intentionally swallowed so the chain can continue.
	apiPrice := func() (float64, bool) {
		switch p.detectedCloud {
		case collector.CloudAWS:
			p.initAWS(ctx)
			if p.aws != nil {
				if price, err := p.aws.getHourlyPrice(ctx, instanceType, region, zone, isSpot); err == nil {
					return price, true
				}
			}
		case collector.CloudAzure:
			if price, err := p.azure.GetHourlyPrice(ctx, instanceType, region, isSpot); err == nil {
				return price, true
			}
		}
		return 0, false
	}

	if price, ok := apiPrice(); ok {
		return price, PriceSourceAPI, false, nil
	}

	if cloudName != "" {
		if price, fallback, err := LookupEmbeddedPrice(cloudName, region, instanceType); err == nil {
			if !isSpot {
				return price, PriceSourceEmbedded, fallback, nil
			}
			// Spot from embedded on-demand: apply advisor discount when known,
			// otherwise the historical fallback multiplier. These are mutually
			// exclusive, never stacked.
			savings, _, ok := lookupSpotAdvisor(instanceType, region)
			if ok && savings > 0 {
				return price * (1 - float64(savings)/100.0), PriceSourceEmbedded, fallback, nil
			}
			return price * SpotFallbackMultiplier, PriceSourceEmbedded, fallback, nil
		}
	}

	// Final layer: static table / suffix estimate.
	price, source := p.fallback.lookup(instanceType, region, isSpot)
	// A static-table hit for an unknown region means the default-region prices
	// were reused, so mark the fallback. Estimations are region-independent.
	// Unknown-cloud/on-prem nodes have no meaningful region, so do not flag
	// them.
	_, regionKnown := p.fallback.prices[region]
	regionFallback := cloudName != "" && region != "" && !regionKnown && source == PriceSourceEmbedded
	if source == PriceSourceEstimated {
		cloudForErr := cloudName
		if cloudForErr == "" {
			cloudForErr = "unknown"
		}
		est := &EstimateError{
			Cloud:        cloudForErr,
			Region:       region,
			InstanceType: instanceType,
			IsSpot:       isSpot,
		}
		if p.strict {
			return 0, source, regionFallback, est
		}
		slog.Debug("pricing used an estimate", "instance_type", instanceType, "region", region, "spot", isSpot)
	}
	return price, source, regionFallback, nil
}

func (p *CloudPricingProvider) GetNodePricing(ctx context.Context, node collector.NodeInfo) (*NodePricing, error) {
	if p.customPricing != nil && node.CloudProvider == collector.CloudUnknown {
		np := &NodePricing{
			CPUCostPerCore: p.customPricing.CPUCostPerCoreHr,
			RAMCostPerGiB:  p.customPricing.RAMCostPerGiBHr,
			GPUCostPerUnit: p.customPricing.GPUCostPerHr,
			Source:         PriceSourceCustom,
		}
		cpuCores := float64(node.CPUAllocatable) / 1000.0
		ramGiB := float64(node.MemAllocatable) / (1024 * 1024 * 1024)
		np.HourlyTotal = np.CPUCostPerCore*cpuCores + np.RAMCostPerGiB*ramGiB + np.GPUCostPerUnit*float64(node.GPUCount)
		return np, nil
	}

	p.detectCloud(node)
	hourly, source, regionFallback, err := p.resolveHourly(ctx, node.InstanceType, node.Region, node.Zone, node.IsSpot)
	if err != nil {
		return nil, err
	}

	if node.GPUCount > 0 {
		cpuPerCore, ramPerGiB, gpuPerUnit := SplitNodeCostWithGPU(hourly, node.CPUAllocatable, node.MemAllocatable, node.GPUCount)
		return &NodePricing{
			HourlyTotal:    hourly,
			CPUCostPerCore: cpuPerCore,
			RAMCostPerGiB:  ramPerGiB,
			GPUCostPerUnit: gpuPerUnit,
			Source:         source,
			RegionFallback: regionFallback,
		}, nil
	}

	cpuPerCore, ramPerGiB := SplitNodeCost(hourly, node.CPUAllocatable, node.MemAllocatable)
	return &NodePricing{
		HourlyTotal:    hourly,
		CPUCostPerCore: cpuPerCore,
		RAMCostPerGiB:  ramPerGiB,
		Source:         source,
		RegionFallback: regionFallback,
	}, nil
}

func (p *CloudPricingProvider) GetStoragePricePerGiBMonth(ctx context.Context, storageClass string) float64 {
	if p.customPricing != nil && p.customPricing.StoragePricePerGiBMo > 0 {
		return p.customPricing.StoragePricePerGiBMo
	}

	region := p.fallback.region

	if region != "" {
		switch p.detectedCloud {
		case collector.CloudAWS:
			p.initAWS(ctx)
			if p.aws != nil {
				price, err := p.aws.GetEBSPrice(ctx, storageClass, region)
				if err == nil && price > 0 {
					return price
				}
			}
		case collector.CloudAzure:
			price, err := p.azure.GetDiskPrice(ctx, storageClass, region)
			if err == nil && price > 0 {
				return price
			}
		}
	}

	return p.fallback.GetStoragePricePerGiBMonth(ctx, storageClass)
}

func (p *CloudPricingProvider) GetLoadBalancerPricePerHour() float64 {
	switch p.detectedCloud {
	case collector.CloudAWS:
		p.initAWS(context.Background())
		if p.aws != nil && p.fallback.region != "" {
			if price, err := p.aws.GetLBPrice(context.Background(), p.fallback.region); err == nil {
				return price
			}
		}
		return 0.0225
	case collector.CloudAzure:
		return 0.005
	case collector.CloudGCP:
		return 0.025
	default:
		return 0.0225
	}
}

func (p *CloudPricingProvider) GetNetworkEgressPricePerGiB() float64 {
	return p.fallback.GetNetworkEgressPricePerGiB()
}

func (p *CloudPricingProvider) GetRISaving(ctx context.Context, instanceType, region string) (saving float64, pct float64, ok bool) {
	if region == "" {
		return 0, 0, false
	}
	switch p.detectedCloud {
	case collector.CloudAWS:
		p.initAWS(ctx)
		if p.aws == nil {
			return 0, 0, false
		}
		riHourly, err := p.aws.GetRIHourlyPrice(ctx, instanceType, region)
		if err != nil {
			return 0, 0, false
		}
		odHourly, err := p.aws.GetHourlyPrice(ctx, instanceType, region, false)
		if err != nil {
			return 0, 0, false
		}
		riMonthly := riHourly * HoursPerMonth
		odMonthly := odHourly * HoursPerMonth
		saving = odMonthly - riMonthly
		if saving > 0 {
			return saving, saving / odMonthly * 100, true
		}
	case collector.CloudAzure:
		riMonthly, err := p.azure.GetRIMonthlyPrice(ctx, instanceType, region)
		if err != nil {
			return 0, 0, false
		}
		odHourly, err := p.azure.GetHourlyPrice(ctx, instanceType, region, false)
		if err != nil {
			return 0, 0, false
		}
		odMonthly := odHourly * HoursPerMonth
		saving = odMonthly - riMonthly
		if saving > 0 {
			return saving, saving / odMonthly * 100, true
		}
	}
	return 0, 0, false
}

func (p *CloudPricingProvider) GetSpotDiscount(ctx context.Context, instanceType, region string) SpotDiscount {
	if region == "" {
		return SpotDiscount{Discount: 0, InterruptionRate: -1, Source: "unavailable"}
	}

	switch p.detectedCloud {
	case collector.CloudAWS:
		p.initAWS(ctx)
		if p.aws != nil {
			spotPrice, err := p.aws.GetHourlyPrice(ctx, instanceType, region, true)
			if err == nil && spotPrice > 0 {
				onDemandPrice, err := p.aws.GetHourlyPrice(ctx, instanceType, region, false)
				if err == nil && onDemandPrice > 0 {
					discount := 1 - (spotPrice / onDemandPrice)
					if discount > 0 && discount < 1 {
						_, ir, ok := lookupSpotAdvisor(instanceType, region)
						irVal := -1
						if ok {
							irVal = ir
						}
						return SpotDiscount{
							Discount:         discount,
							InterruptionRate: irVal,
							Source:           "api",
						}
					}
				}
			}
		}
	case collector.CloudAzure:
		spotPrice, err := p.azure.GetHourlyPrice(ctx, instanceType, region, true)
		if err == nil && spotPrice > 0 {
			onDemandPrice, err := p.azure.GetHourlyPrice(ctx, instanceType, region, false)
			if err == nil && onDemandPrice > 0 {
				discount := 1 - (spotPrice / onDemandPrice)
				if discount > 0 && discount < 1 {
					return SpotDiscount{
						Discount:         discount,
						InterruptionRate: -1,
						Source:           "api",
					}
				}
			}
		}
	}

	savings, ir, ok := lookupSpotAdvisor(instanceType, region)
	if ok && savings > 0 {
		return SpotDiscount{
			Discount:         float64(savings) / 100.0,
			InterruptionRate: ir,
			Source:           "advisor",
		}
	}

	return SpotDiscount{
		Discount:         0,
		InterruptionRate: -1,
		Source:           "unavailable",
	}
}
