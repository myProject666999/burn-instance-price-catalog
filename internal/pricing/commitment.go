package pricing

import (
	"fmt"
	"math"
)

// CommitmentTerms describes a purchased commitment (Reserved Instance,
// Savings Plan, Committed Use Discount) that covers a fraction of a given
// instance type's usage.
//
// Coverage is expressed as a fraction of node count for an instance type in a
// region, because Kubernetes does not reveal which specific nodes the provider
// applied the commitment to. Callers must therefore report a blended average
// price and must never label an individual node as "covered".
type CommitmentTerms struct {
	// OnDemandHourly is the undiscounted hourly price.
	OnDemandHourly float64
	// CommitHourly is the hourly price under the commitment.
	CommitHourly float64
	// Coverage in [0, 1]: the fraction of instances of this type that the
	// commitment covers.
	Coverage float64
}

// Validate checks the commitment terms for impossible values.
func (c CommitmentTerms) Validate() error {
	if c.OnDemandHourly < 0 || c.CommitHourly < 0 {
		return fmt.Errorf("commitment prices must not be negative: on-demand=%v commit=%v", c.OnDemandHourly, c.CommitHourly)
	}
	if c.Coverage < 0 || c.Coverage > 1 {
		return fmt.Errorf("commitment coverage must be in [0, 1]: %v", c.Coverage)
	}
	if c.CommitHourly > c.OnDemandHourly {
		return fmt.Errorf("commitment price %v exceeds on-demand price %v", c.CommitHourly, c.OnDemandHourly)
	}
	return nil
}

// BlendedResult is a coverage-weighted average over an instance type's nodes.
type BlendedResult struct {
	// BlendHourly is the weighted average hourly price per instance.
	BlendHourly float64
	// CoveredCount and OnDemandCount are the implied node counts after
	// applying Coverage to NodeCount.
	CoveredCount  float64
	OnDemandCount float64
	// Coverage echoes the input coverage in [0, 1].
	Coverage float64
}

// Blend computes the mixed average hourly price for nodeCount instances of one
// type when a fraction Coverage are on the commitment price and the rest on
// demand. E.g. 6 nodes at 4/6 coverage: 4 pay CommitHourly, 2 pay
// OnDemandHourly.
//
// The result is a blended rate; it does not identify which nodes are covered.
// Returns an error for invalid terms or a non-positive node count.
func Blend(nodeCount int, terms CommitmentTerms) (BlendedResult, error) {
	if nodeCount < 0 {
		return BlendedResult{}, fmt.Errorf("node count must not be negative: %d", nodeCount)
	}
	if err := terms.Validate(); err != nil {
		return BlendedResult{}, err
	}
	if nodeCount == 0 {
		return BlendedResult{}, fmt.Errorf("cannot blend commitments over zero nodes")
	}

	covered := float64(nodeCount) * terms.Coverage
	onDemand := float64(nodeCount) - covered

	blend := (covered*terms.CommitHourly + onDemand*terms.OnDemandHourly) / float64(nodeCount)
	blend = math.Max(0, blend)

	return BlendedResult{
		BlendHourly:   blend,
		CoveredCount:  covered,
		OnDemandCount: onDemand,
		Coverage:      terms.Coverage,
	}, nil
}

// BlendDiscount returns the implied discount fraction of the blended rate
// versus on-demand, clamped to [0, 1). Useful for reporting.
func (r BlendedResult) BlendDiscount(onDemandHourly float64) float64 {
	if onDemandHourly <= 0 {
		return 0
	}
	d := 1 - r.BlendHourly/onDemandHourly
	if d < 0 {
		return 0
	}
	if d >= 1 {
		return math.Nextafter(1, 0)
	}
	return d
}
