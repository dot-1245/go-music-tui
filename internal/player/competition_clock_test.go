package player

import (
	"math"
	"testing"
	"time"
)

func competitionSample(at time.Time, position, rate float64, track, status string, generation uint64) PositionSample {
	return PositionSample{
		Position: position, Rate: rate, Track: track, Status: status,
		StartedAt: at, FinishedAt: at, Generation: generation,
	}
}

func requireCompetitionPosition(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > tolerance {
		t.Fatalf("position = %.6f, want %.6f ± %.6f", got, want, tolerance)
	}
}

func TestCompetitionClockMethodsFollowTrueRate(t *testing.T) {
	base := time.Now()
	for _, tc := range []struct {
		method CompetitionMethod
		rate   float64
	}{
		{SyncPeriodic, .5}, {SyncPeriodic, 1.5},
		{SyncGentle, .5}, {SyncGentle, 1.5},
		{SyncMedian, .5}, {SyncMedian, 1.5},
		{SyncPLL, .5}, {SyncPLL, 1.5},
	} {
		t.Run(string(tc.method)+"/rate-"+map[float64]string{.5: "half", 1.5: "one-and-a-half"}[tc.rate], func(t *testing.T) {
			clock := CompetitionClock{Method: tc.method}
			if !clock.Transport(competitionSample(base, 7, tc.rate, "A", "Playing", 1)) {
				t.Fatal("initial sample rejected")
			}
			requireCompetitionPosition(t, clock.PositionAt(base.Add(2*time.Second)), 7+2*tc.rate, 1e-9)
		})
	}
}

func TestCompetitionClockPauseIsConstant(t *testing.T) {
	base := time.Now()
	clock := CompetitionClock{Method: SyncPeriodic}
	if !clock.Transport(competitionSample(base, 42.25, 1.5, "A", "Paused", 1)) {
		t.Fatal("paused sample rejected")
	}
	requireCompetitionPosition(t, clock.PositionAt(base.Add(time.Hour)), 42.25, 0)
}

func TestCompetitionClockTransportResetsSameTrackRepeat(t *testing.T) {
	base := time.Now()
	clock := CompetitionClock{Method: SyncGentle}
	if !clock.Transport(competitionSample(base, 180, 1, "A", "Playing", 1)) {
		t.Fatal("initial sample rejected")
	}
	repeatAt := base.Add(time.Second)
	if !clock.Transport(competitionSample(repeatAt, .2, 1, "A", "Playing", 2)) {
		t.Fatal("same-track repeat transport was rejected")
	}
	requireCompetitionPosition(t, clock.PositionAt(repeatAt), .2, 0)
	requireCompetitionPosition(t, clock.PositionAt(repeatAt.Add(time.Second)), 1.2, 1e-9)
}

func TestCompetitionClockRejectsOlderGenerationAndOutOfOrderReplies(t *testing.T) {
	base := time.Now()
	clock := CompetitionClock{Method: SyncPeriodic}
	newer := competitionSample(base.Add(time.Second), 20, 1, "A", "Playing", 3)
	if !clock.Transport(newer) {
		t.Fatal("newer transport sample rejected")
	}
	staleGeneration := competitionSample(base.Add(2*time.Second), 30, 1, "A", "Playing", 2)
	if clock.Observe(staleGeneration) {
		t.Fatal("reply from an older generation was accepted")
	}
	olderFinish := competitionSample(base.Add(500*time.Millisecond), 10, 1, "A", "Playing", 3)
	if clock.Observe(olderFinish) {
		t.Fatal("out-of-order reply was accepted")
	}
	// An overlapping request is also stale when it began before the current
	// anchor, even if it happened to finish later.
	overlapping := competitionSample(base.Add(900*time.Millisecond), 21, 1, "A", "Playing", 3)
	overlapping.FinishedAt = base.Add(2 * time.Second)
	if clock.Observe(overlapping) {
		t.Fatal("overlapping reply was accepted")
	}
	requireCompetitionPosition(t, clock.PositionAt(base.Add(2*time.Second)), 21, 1e-9)
}

func TestCompetitionClockRejectsInvalidFiniteValuesAndIntervals(t *testing.T) {
	base := time.Now()
	valid := competitionSample(base, 1, 1, "A", "Playing", 1)
	invalid := []PositionSample{
		func() PositionSample { s := valid; s.Position = math.NaN(); return s }(),
		func() PositionSample { s := valid; s.Position = math.Inf(1); return s }(),
		func() PositionSample { s := valid; s.Position = -1; return s }(),
		func() PositionSample { s := valid; s.Rate = math.NaN(); return s }(),
		func() PositionSample { s := valid; s.Rate = math.Inf(-1); return s }(),
		func() PositionSample { s := valid; s.Rate = 0; return s }(),
		func() PositionSample { s := valid; s.Track = ""; return s }(),
		func() PositionSample { s := valid; s.Status = "Buffering"; return s }(),
		func() PositionSample { s := valid; s.FinishedAt = base.Add(-time.Nanosecond); return s }(),
	}
	for i, sample := range invalid {
		clock := CompetitionClock{Method: SyncPeriodic}
		if clock.Transport(sample) || clock.Observe(sample) {
			t.Errorf("invalid sample %d was accepted", i)
		}
	}
}

func TestCompetitionClockGentleSettlesBackwardCorrectionMonotonically(t *testing.T) {
	base := time.Now()
	clock := CompetitionClock{Method: SyncGentle}
	if !clock.Transport(competitionSample(base, 30, 1, "A", "Playing", 1)) {
		t.Fatal("initial sample rejected")
	}
	updateAt := base.Add(100 * time.Millisecond)
	if !clock.Observe(competitionSample(updateAt, 29.9, 1, "A", "Playing", 1)) {
		t.Fatal("small backward correction rejected")
	}
	start := clock.PositionAt(updateAt)
	mid := clock.PositionAt(updateAt.Add(250 * time.Millisecond))
	end := clock.PositionAt(updateAt.Add(500 * time.Millisecond))
	if !(start < mid && mid < end) {
		t.Fatalf("settle was not monotonic: start=%.6f mid=%.6f end=%.6f", start, mid, end)
	}
	requireCompetitionPosition(t, end, 30.4, 1e-9)
}

func TestCompetitionClockMedianRejectsIsolatedBadSampleButFollowsSeek(t *testing.T) {
	base := time.Now()
	clock := CompetitionClock{Method: SyncMedian}
	if !clock.Transport(competitionSample(base, 10, 1, "A", "Playing", 1)) {
		t.Fatal("initial sample rejected")
	}
	observe := func(seconds int, position float64) {
		t.Helper()
		at := base.Add(time.Duration(seconds) * time.Second)
		if !clock.Observe(competitionSample(at, position, 1, "A", "Playing", 1)) {
			t.Fatalf("sample at %ds rejected", seconds)
		}
	}
	observe(1, 11)
	observe(2, 20) // one isolated erroneous position
	requireCompetitionPosition(t, clock.PositionAt(base.Add(2*time.Second)), 12, 1e-9)
	observe(3, 15) // first real post-seek observation
	observe(4, 16) // second real post-seek observation
	requireCompetitionPosition(t, clock.PositionAt(base.Add(4*time.Second)), 16, 1e-9)
}

func TestCompetitionClockPLLBoundsCorrectionAndReanchorsLargeSeek(t *testing.T) {
	base := time.Now()
	clock := CompetitionClock{Method: SyncPLL}
	if !clock.Transport(competitionSample(base, 10, 1, "A", "Playing", 1)) {
		t.Fatal("initial sample rejected")
	}
	if !clock.Observe(competitionSample(base.Add(time.Second), 10.9, 1, "A", "Playing", 1)) {
		t.Fatal("small phase error rejected")
	}
	if math.Abs(clock.rateCorrection) > .01 {
		t.Fatalf("PLL correction %.6f exceeds bound ±0.01", clock.rateCorrection)
	}
	seekAt := base.Add(2 * time.Second)
	if !clock.Observe(competitionSample(seekAt, 50, 1, "A", "Playing", 1)) {
		t.Fatal("large seek observation rejected")
	}
	if clock.rateCorrection != 0 {
		t.Fatalf("large seek retained PLL correction %.6f", clock.rateCorrection)
	}
	requireCompetitionPosition(t, clock.PositionAt(seekAt), 50, 1e-9)
	requireCompetitionPosition(t, clock.PositionAt(seekAt.Add(time.Second)), 51, 1e-9)
}
