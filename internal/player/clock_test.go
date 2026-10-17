package player

import (
	"math"
	"testing"
	"time"
)

func clockObservation(start time.Time, rtt time.Duration, position float64, title, status string) Observation {
	finish := start.Add(rtt)
	return Observation{
		Snapshot: Snapshot{Info: Info{
			Title: title, Artist: "Artist", Album: "Album", Status: status,
			LengthSeconds: 240, PositionSeconds: position,
		}},
		StartedAt: start, FinishedAt: finish,
	}
}

func requirePosition(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tolerance {
		t.Fatalf("position = %.6f, want %.6f ± %.6f", got, want, tolerance)
	}
}

func TestPlaybackClockRejectsInvalidAndOutOfOrderObservations(t *testing.T) {
	base := time.Now()
	clock := PlaybackClock{Strategy: ClockSlew}
	valid := clockObservation(base, 20*time.Millisecond, 12, "Track", "Playing")
	if !clock.Observe(valid) {
		t.Fatal("valid observation was rejected")
	}

	invalid := []Observation{
		clockObservation(base.Add(time.Second), time.Millisecond, math.NaN(), "Track", "Playing"),
		clockObservation(base.Add(time.Second), time.Millisecond, math.Inf(1), "Track", "Playing"),
		clockObservation(base.Add(time.Second), time.Millisecond, -0.01, "Track", "Playing"),
		{Snapshot: Snapshot{Info: Info{PositionSeconds: 1}}, StartedAt: base.Add(time.Second)},
		clockObservation(base.Add(2*time.Second), -time.Millisecond, 1, "Track", "Playing"),
		clockObservation(base.Add(-time.Second), 5*time.Millisecond, 1, "Track", "Playing"),
	}
	for i, observation := range invalid {
		if clock.Observe(observation) {
			t.Errorf("invalid observation %d was accepted", i)
		}
	}
	if clock.Observe(valid) {
		t.Fatal("observation finishing at the last accepted timestamp was accepted")
	}
	requirePosition(t, clock.PositionAt(base.Add(120*time.Millisecond)), 12.11, 0.002)
}

func TestPlaybackClockMidpointIsAnEstimateWithRTTUncertainty(t *testing.T) {
	base := time.Now()
	const sampledPosition = 20.0
	const rtt = 200 * time.Millisecond

	for _, strategy := range []ClockStrategy{ClockReceipt, ClockMidpoint} {
		clock := PlaybackClock{Strategy: strategy}
		observation := clockObservation(base, rtt, sampledPosition, "Track", "Playing")
		if !clock.Observe(observation) {
			t.Fatalf("%s rejected observation", strategy)
		}
		want := sampledPosition
		if strategy == ClockMidpoint {
			want += rtt.Seconds() / 2
		}
		requirePosition(t, clock.PositionAt(observation.FinishedAt), want, 0.0001)
	}

	// The server may sample anywhere in the request interval. Midpoint is
	// centered only under a symmetry assumption; the actual error can approach
	// half the RTT when the call's latency is asymmetric.
	midpointEstimate := sampledPosition + rtt.Seconds()/2
	actualIfSampledAtStart := sampledPosition + rtt.Seconds()
	actualIfSampledAtFinish := sampledPosition
	if math.Abs(midpointEstimate-actualIfSampledAtStart) > rtt.Seconds()/2+1e-9 ||
		math.Abs(midpointEstimate-actualIfSampledAtFinish) > rtt.Seconds()/2+1e-9 {
		t.Fatal("midpoint estimate escaped its stated half-RTT uncertainty bound")
	}
}

func TestPlaybackClockBoundedSlewRejectsSlowRTT(t *testing.T) {
	base := time.Now()
	clock := PlaybackClock{Strategy: ClockBounded}
	fast := clockObservation(base, 250*time.Millisecond, 5, "Track", "Playing")
	if !clock.Observe(fast) {
		t.Fatal("RTT at the bound should be accepted")
	}
	slow := clockObservation(base.Add(time.Second), 251*time.Millisecond, 6, "Track", "Playing")
	if clock.Observe(slow) {
		t.Fatal("RTT above the bound should be rejected")
	}
	requirePosition(t, clock.PositionAt(slow.FinishedAt), 5+slow.FinishedAt.Sub(base.Add(125*time.Millisecond)).Seconds(), 0.002)
}

func TestPlaybackClockPauseAndResume(t *testing.T) {
	base := time.Now()
	clock := PlaybackClock{Strategy: ClockSlew}
	playing := clockObservation(base, 20*time.Millisecond, 10, "Track", "Playing")
	if !clock.Observe(playing) {
		t.Fatal("initial playing observation rejected")
	}
	paused := clockObservation(playing.FinishedAt.Add(time.Second), 20*time.Millisecond, 11, "Track", "Paused")
	if !clock.Observe(paused) {
		t.Fatal("pause observation rejected")
	}
	requirePosition(t, clock.PositionAt(paused.FinishedAt.Add(5*time.Second)), 11, 0.0001)

	resumed := clockObservation(paused.FinishedAt.Add(6*time.Second), 20*time.Millisecond, 11, "Track", "Playing")
	if !clock.Observe(resumed) {
		t.Fatal("resume observation rejected")
	}
	requirePosition(t, clock.PositionAt(resumed.FinishedAt.Add(time.Second)), 12.01, 0.0001)
}

func TestPlaybackClockTrackChangeReanchorsImmediately(t *testing.T) {
	base := time.Now()
	clock := PlaybackClock{Strategy: ClockSlew}
	first := clockObservation(base, 20*time.Millisecond, 100, "Old", "Playing")
	if !clock.Observe(first) {
		t.Fatal("initial observation rejected")
	}
	next := clockObservation(first.FinishedAt.Add(time.Second), 20*time.Millisecond, 0.4, "New", "Playing")
	if !clock.Observe(next) {
		t.Fatal("track change observation rejected")
	}
	requirePosition(t, clock.PositionAt(next.FinishedAt), 0.41, 0.0001)
}

func TestPlaybackClockSeekAndRepeatBeyondQuarterSecondReanchor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		initial  float64
		observed float64
	}{
		{name: "backward seek", initial: 50, observed: 49.5},
		{name: "forward seek", initial: 10, observed: 10.5},
		{name: "repeat reset", initial: 239.9, observed: 0.2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := time.Now()
			clock := PlaybackClock{Strategy: ClockSlew}
			first := clockObservation(base, 10*time.Millisecond, tc.initial, "Track", "Playing")
			if !clock.Observe(first) {
				t.Fatal("initial observation rejected")
			}
			next := clockObservation(first.FinishedAt.Add(100*time.Millisecond), 10*time.Millisecond, tc.observed, "Track", "Playing")
			if !clock.Observe(next) {
				t.Fatal("seek/repeat observation rejected")
			}
			requirePosition(t, clock.PositionAt(next.FinishedAt), tc.observed+0.005, 0.0001)
		})
	}
}

func TestPlaybackClockSlewConvergesAndKeepsSmallBackwardCorrectionMonotonic(t *testing.T) {
	base := time.Now()
	clock := PlaybackClock{Strategy: ClockSlew}
	first := clockObservation(base, 10*time.Millisecond, 30, "Track", "Playing")
	if !clock.Observe(first) {
		t.Fatal("initial observation rejected")
	}
	// At finish, the previous estimate is 30.01s. A sample of 29.90s
	// creates a small backward correction which should be slewed over 500ms.
	next := clockObservation(first.FinishedAt.Add(100*time.Millisecond), 10*time.Millisecond, 29.90, "Track", "Playing")
	if !clock.Observe(next) {
		t.Fatal("small backward correction rejected")
	}
	start := next.FinishedAt
	startPosition := clock.PositionAt(start)
	midPosition := clock.PositionAt(start.Add(250 * time.Millisecond))
	endPosition := clock.PositionAt(start.Add(500 * time.Millisecond))
	requirePosition(t, startPosition, 30.115, 0.002)
	requirePosition(t, endPosition, 30.405, 0.002)
	if midPosition <= startPosition || endPosition <= midPosition {
		t.Fatalf("slewed clock was not monotonic: start=%.6f mid=%.6f end=%.6f", startPosition, midPosition, endPosition)
	}
	// After the settling window, the raw clock remains anchored to the latest
	// observation without a residual offset.
	requirePosition(t, clock.PositionAt(start.Add(time.Second)), 30.905, 0.002)
}

func TestPlaybackClockIntervalLeavesEstimateInsideRTTBoundsAlone(t *testing.T) {
	base := time.Now()
	clock := PlaybackClock{Strategy: ClockInterval}
	first := clockObservation(base, 0, 10, "Track", "Playing")
	if !clock.Observe(first) {
		t.Fatal("initial observation rejected")
	}
	next := clockObservation(base.Add(time.Second), 200*time.Millisecond, 11.1, "Track", "Playing")
	if !clock.Observe(next) {
		t.Fatal("observation within RTT interval rejected")
	}
	// Previous estimate at finish is 11.2, inside [11.1, 11.3]. The interval
	// strategy should retain it rather than chase the midpoint or reported value.
	requirePosition(t, clock.PositionAt(next.FinishedAt), 11.2, 0.0001)
}

func TestPlaybackClockIntervalCorrectsOutsideBoundsAndSlews(t *testing.T) {
	base := time.Now()
	clock := PlaybackClock{Strategy: ClockInterval}
	first := clockObservation(base, 0, 10, "Track", "Playing")
	if !clock.Observe(first) {
		t.Fatal("initial observation rejected")
	}
	next := clockObservation(base.Add(time.Second), 200*time.Millisecond, 10.8, "Track", "Playing")
	if !clock.Observe(next) {
		t.Fatal("observation outside RTT interval rejected")
	}
	start := next.FinishedAt
	// Previous estimate is 11.2 while the interval ends at 11.0, so the
	// corrected anchor is 11.0 with a +0.2s settling offset.
	requirePosition(t, clock.PositionAt(start), 11.2, 0.0001)
	requirePosition(t, clock.PositionAt(start.Add(250*time.Millisecond)), 11.35, 0.0001)
	requirePosition(t, clock.PositionAt(start.Add(500*time.Millisecond)), 11.5, 0.0001)
}

func TestPlaybackClockIntervalRejectsSlowRTTAndReanchorsDiscontinuities(t *testing.T) {
	base := time.Now()
	clock := PlaybackClock{Strategy: ClockInterval}
	first := clockObservation(base, 0, 30, "Old", "Playing")
	if !clock.Observe(first) {
		t.Fatal("initial observation rejected")
	}
	slow := clockObservation(base.Add(time.Second), 251*time.Millisecond, 31, "Old", "Playing")
	if clock.Observe(slow) {
		t.Fatal("RTT above 250ms should be rejected")
	}

	changed := clockObservation(base.Add(2*time.Second), 200*time.Millisecond, 4, "New", "Playing")
	if !clock.Observe(changed) {
		t.Fatal("new-track observation rejected")
	}
	// Track changes use the midpoint anchor directly, without carrying the old
	// track's estimate or slew offset across the boundary.
	requirePosition(t, clock.PositionAt(changed.FinishedAt), 4.1, 0.0001)
}
