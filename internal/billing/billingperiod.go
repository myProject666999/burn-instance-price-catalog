package billing

import "time"

// BillingTimingConfig describes how a provider bills a node over a window.
// Every field defaults to "no extra rule" so callers must opt into
// provider-specific quirks rather than inheriting them.
type BillingTimingConfig struct {
	// MinimumCharge is the smallest billed duration for one continuous run of
	// the node. Zero disables the rule.
	//
	// Cross-window apportionment: the minimum is charged exactly once, in the
	// window that observes the end of the node's life, and is computed from the
	// node's total lifetime. Earlier windows bill only real elapsed time. A
	// node still alive at the end of the data therefore never has the minimum
	// applied in a partial window (it will be applied when its death is seen).
	// This is what prevents re-charging the minimum in every window.
	MinimumCharge time.Duration
	// RoundingGranularity rounds the billed seconds up to a multiple of this
	// duration. Zero means no rounding.
	RoundingGranularity time.Duration
}

// BillingWindow is the result of pricing one node over one window.
type BillingWindow struct {
	Seconds int64
	// CreatedUnknown is true when createdAt was zero and the whole window was
	// billed.
	CreatedUnknown bool
	// ClampedToWindow is true when the node's life extended beyond the window
	// and was clamped to the window edges.
	ClampedToWindow bool
	// MinimumApplied is true when the minimum charge raised this window's
	// seconds above the real elapsed time.
	MinimumApplied bool
}

// BillableSeconds computes the billed seconds for a node whose lifetime is
// [createdAt, aliveUntil) intersect the window [windowStart, windowEnd).
//
// Rules:
//   - A zero createdAt is treated as "alive for the whole window" and reported
//     via CreatedUnknown; no minimum is inferred because the lifetime is
//     unknowable.
//   - Only the overlap between the node's life and the window is billable.
//   - RoundingGranularity rounds that overlap up.
//   - MinimumCharge is applied only in the window where the node's death is
//     observed (aliveUntil non-zero and within the window). The uplift is
//     max(0, minimum - totalLifetime), so the minimum is charged once for the
//     whole life, not once per window.
//
// The returned seconds are never negative.
func BillableSeconds(createdAt, aliveUntil, windowStart, windowEnd time.Time, cfg BillingTimingConfig) BillingWindow {
	if !windowEnd.After(windowStart) {
		return BillingWindow{}
	}

	lifeStart := createdAt
	createdUnknown := false
	if createdAt.IsZero() {
		// No creation timestamp: assume the node existed for the whole window
		// and say so.
		lifeStart = windowStart
		createdUnknown = true
	}

	// Whether the node's death is observed inside this window. Only then can
	// the total lifetime be known and the minimum apportioned.
	deathObserved := !aliveUntil.IsZero() && !aliveUntil.After(windowEnd)

	lifeEnd := aliveUntil
	if lifeEnd.IsZero() || lifeEnd.After(windowEnd) {
		lifeEnd = windowEnd
	}

	// Intersect life with the window.
	start := lifeStart
	if start.Before(windowStart) {
		start = windowStart
	}
	end := lifeEnd
	if end.After(windowEnd) {
		end = windowEnd
	}
	if !end.After(start) {
		return BillingWindow{CreatedUnknown: createdUnknown}
	}

	clamped := lifeStart.Before(windowStart) || (aliveUntil.IsZero() || aliveUntil.After(windowEnd))

	secs := end.Sub(start).Seconds()
	minimumApplied := false

	// Apply the minimum once, in the window that sees the node die, based on
	// its total lifetime.
	if cfg.MinimumCharge > 0 && deathObserved && !createdUnknown {
		totalLife := aliveUntil.Sub(createdAt).Seconds()
		if shortfall := cfg.MinimumCharge.Seconds() - totalLife; shortfall > 0 {
			secs += shortfall
			minimumApplied = true
		}
	}

	// Round up to the granularity.
	if cfg.RoundingGranularity > 0 {
		gran := cfg.RoundingGranularity.Seconds()
		secs = ceilTo(secs, gran)
	}

	if secs < 0 {
		secs = 0
	}

	return BillingWindow{
		Seconds:         int64(secs),
		CreatedUnknown:  createdUnknown,
		ClampedToWindow: clamped,
		MinimumApplied:  minimumApplied,
	}
}

// ceilTo rounds v up to the nearest multiple of step. v and step are seconds.
func ceilTo(v, step float64) float64 {
	if step <= 0 {
		return v
	}
	quotient := v / step
	// Guard against float imprecision when v is already a multiple.
	if quotient == float64(int64(quotient)) {
		return v
	}
	whole := float64(int64(quotient))
	if quotient < 0 {
		return whole * step
	}
	return (whole + 1) * step
}
