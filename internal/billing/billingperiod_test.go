package billing

import (
	"testing"
	"time"
)

func base(t time.Time, d time.Duration) time.Time { return t.Add(d) }

func TestBillableSeconds_ShortLived(t *testing.T) {
	winStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	winEnd := base(winStart, 24*time.Hour)

	// Node created 30 seconds before the window ends.
	created := base(winEnd, -30*time.Second)
	got := BillableSeconds(created, time.Time{}, winStart, winEnd, BillingTimingConfig{})
	if got.Seconds != 30 {
		t.Errorf("seconds = %d, want 30", got.Seconds)
	}
	if got.CreatedUnknown {
		t.Error("CreatedUnknown should be false")
	}
}

func TestBillableSeconds_SixtyOneSeconds(t *testing.T) {
	winStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	winEnd := base(winStart, 24*time.Hour)
	created := base(winEnd, -61*time.Second)

	got := BillableSeconds(created, time.Time{}, winStart, winEnd, BillingTimingConfig{})
	if got.Seconds != 61 {
		t.Errorf("seconds = %d, want 61", got.Seconds)
	}
}

func TestBillableSeconds_MinimumCharge(t *testing.T) {
	winStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	winEnd := base(winStart, 24*time.Hour)
	// AWS Linux on-demand has a 60-second minimum; use it as an explicit config.
	cfg := BillingTimingConfig{MinimumCharge: 60 * time.Second}

	// Node lived 30s and died within the window: billed 60s.
	created := base(winStart, time.Hour)
	aliveUntil := base(created, 30*time.Second)
	got := BillableSeconds(created, aliveUntil, winStart, winEnd, cfg)
	if got.Seconds != 60 {
		t.Errorf("seconds = %d, want 60 (minimum)", got.Seconds)
	}
	if !got.MinimumApplied {
		t.Error("MinimumApplied should be true")
	}

	// Node lived 90s: minimum does not apply.
	aliveUntil = base(created, 90*time.Second)
	got = BillableSeconds(created, aliveUntil, winStart, winEnd, cfg)
	if got.Seconds != 90 {
		t.Errorf("seconds = %d, want 90", got.Seconds)
	}
	if got.MinimumApplied {
		t.Error("MinimumApplied should be false")
	}
}

func TestBillableSeconds_MinimumNotRechargedAcrossWindows(t *testing.T) {
	// The minimum must be charged exactly once across a node's whole life, not
	// once per window. Simulate a 40s life entirely inside one window split
	// across two adjacent windows, and also a node spanning from one window
	// into the next.
	cfg := BillingTimingConfig{MinimumCharge: 60 * time.Second}
	w1Start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w1End := base(w1Start, time.Minute)
	w2Start := w1End
	w2End := base(w2Start, time.Minute)

	// Node created 10s into window 1, alive 40s: dies at 50s, inside window 1.
	created := base(w1Start, 10*time.Second)
	aliveUntil := base(created, 40*time.Second)

	w1 := BillableSeconds(created, aliveUntil, w1Start, w1End, cfg)
	w2 := BillableSeconds(created, aliveUntil, w2Start, w2End, cfg)

	// Window 1 sees the death, so it bills the 60s minimum once.
	if w1.Seconds != 60 {
		t.Errorf("window1 seconds = %d, want 60 (minimum)", w1.Seconds)
	}
	// Window 2 has no overlap.
	if w2.Seconds != 0 {
		t.Errorf("window2 seconds = %d, want 0", w2.Seconds)
	}
	if !w1.MinimumApplied || w2.MinimumApplied {
		t.Errorf("minimum applied in wrong window: w1=%v w2=%v", w1.MinimumApplied, w2.MinimumApplied)
	}

	// Total billed across windows == minimum, not 2x minimum.
	if w1.Seconds+w2.Seconds != 60 {
		t.Errorf("total billed = %d, want 60", w1.Seconds+w2.Seconds)
	}
}

func TestBillableSeconds_CrossWindowLongLife(t *testing.T) {
	cfg := BillingTimingConfig{MinimumCharge: 60 * time.Second}
	w1Start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w1End := base(w1Start, time.Hour)
	w2Start := w1End
	w2End := base(w2Start, time.Hour)

	// Created in window 1, alive for 90 minutes (dies in window 2).
	created := base(w1Start, 30*time.Minute) // 30 min into w1
	aliveUntil := base(created, 90*time.Minute)

	w1 := BillableSeconds(created, aliveUntil, w1Start, w1End, cfg)
	w2 := BillableSeconds(created, aliveUntil, w2Start, w2End, cfg)

	// w1 bills 30 min of real time, no minimum (death not observed).
	if w1.Seconds != int64(30*time.Minute.Seconds()) {
		t.Errorf("window1 seconds = %d, want 1800", w1.Seconds)
	}
	if w1.MinimumApplied {
		t.Error("minimum should not apply in w1 (death not observed)")
	}
	// w2 bills 45 min of real time (from w2Start to death at +60min into w2...
	// wait: created at +30m, life 90m => dies at +120m = 60m into w2).
	if w2.Seconds != int64(60*time.Minute.Seconds()) {
		t.Errorf("window2 seconds = %d, want 3600", w2.Seconds)
	}
	// Total = 30m + 60m = 90m real; minimum irrelevant since life >= 60s.
}

func TestBillableSeconds_ZeroCreatedAtWholeWindow(t *testing.T) {
	winStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	winEnd := base(winStart, 24*time.Hour)

	got := BillableSeconds(time.Time{}, time.Time{}, winStart, winEnd, BillingTimingConfig{MinimumCharge: 60 * time.Second})
	if got.Seconds != int64(24*time.Hour.Seconds()) {
		t.Errorf("seconds = %d, want full window", got.Seconds)
	}
	if !got.CreatedUnknown {
		t.Error("CreatedUnknown should be true for zero createdAt")
	}
	if got.MinimumApplied {
		t.Error("minimum must not apply when creation is unknown")
	}
}

func TestBillableSeconds_WholeHourRounding(t *testing.T) {
	winStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	winEnd := base(winStart, 24*time.Hour)
	cfg := BillingTimingConfig{RoundingGranularity: time.Hour}

	tests := []struct {
		name    string
		lived   time.Duration
		wantSec int64
	}{
		{"1 second rounds up to 1h", time.Second, 3600},
		{"exactly 1h stays 1h", time.Hour, 3600},
		{"1h+1s rounds to 2h", time.Hour + time.Second, 7200},
		{"61s rounds to 1h", 61 * time.Second, 3600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := winStart
			aliveUntil := base(created, tt.lived)
			got := BillableSeconds(created, aliveUntil, winStart, winEnd, cfg)
			if got.Seconds != tt.wantSec {
				t.Errorf("seconds = %d, want %d", got.Seconds, tt.wantSec)
			}
		})
	}
}

func TestBillableSeconds_ClampAndEmpty(t *testing.T) {
	winStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	winEnd := base(winStart, time.Hour)

	// Node created before the window and alive past it: clamped.
	created := base(winStart, -time.Hour)
	aliveUntil := base(winEnd, time.Hour)
	got := BillableSeconds(created, aliveUntil, winStart, winEnd, BillingTimingConfig{})
	if got.Seconds != 3600 {
		t.Errorf("seconds = %d, want 3600", got.Seconds)
	}
	if !got.ClampedToWindow {
		t.Error("ClampedToWindow should be true")
	}

	// Node not alive during the window at all.
	got = BillableSeconds(base(winEnd, time.Minute), base(winEnd, 2*time.Minute), winStart, winEnd, BillingTimingConfig{})
	if got.Seconds != 0 {
		t.Errorf("seconds = %d, want 0", got.Seconds)
	}

	// Degenerate window.
	got = BillableSeconds(winStart, winEnd, winEnd, winStart, BillingTimingConfig{})
	if got.Seconds != 0 {
		t.Errorf("seconds = %d, want 0 for inverted window", got.Seconds)
	}
}
