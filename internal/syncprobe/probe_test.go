package syncprobe

import (
	"math"
	"testing"
	"time"

	"github.com/dot-1245/go-music-tui/internal/player"
)

func traceObservation(at time.Time, position float64, status string) player.Observation {
	return player.Observation{Snapshot: player.Snapshot{Info: player.Info{Name: "player", Title: "track", Status: status, PositionSeconds: position}}, StartedAt: at, FinishedAt: at}
}

func TestValidationSamplesDoNotLeakIntoClock(t *testing.T) {
	base := time.Unix(1700000000, 0)
	observations := []player.Observation{traceObservation(base, 10, "Playing"), traceObservation(base.Add(time.Second), 100, "Playing"), traceObservation(base.Add(2*time.Second), 12, "Playing")}
	for _, m := range DisagreementEvery(observations, 2) {
		if m.Samples != 2 || math.Abs(m.MeanMS-44500) > .001 || math.Abs(m.MaxMS-89000) > .001 {
			t.Fatalf("validation sample leaked into estimator: %+v", m)
		}
	}
}

func TestPausedSamplesDoNotImproveRunningClockScore(t *testing.T) {
	base := time.Unix(1700000000, 0)
	observations := []player.Observation{traceObservation(base, 10, "Playing"), traceObservation(base.Add(time.Second), 11, "Playing"), traceObservation(base.Add(2*time.Second), 11, "Paused"), traceObservation(base.Add(3*time.Second), 11, "Paused"), traceObservation(base.Add(4*time.Second), 11, "Playing"), traceObservation(base.Add(5*time.Second), 12, "Playing")}
	for _, m := range Disagreement(observations) {
		if m.Samples != 2 {
			t.Fatalf("paused or transition sample counted: %+v", m)
		}
	}
}

func TestFollowNeverUsesFutureNotification(t *testing.T) {
	base := time.Unix(1700000000, 0)
	observations := []player.Observation{traceObservation(base, 10, "Playing"), traceObservation(base.Add(time.Second), 11, "Playing")}
	future := observations[1].Snapshot
	future.ReceivedAt = base.Add(2 * time.Second)
	if m := FollowDisagreement(observations, []player.Snapshot{future}); m.Samples != 0 {
		t.Fatalf("future snapshot was used: %+v", m)
	}
}

func TestSimulationExposesLatencyAndMeasurementLimits(t *testing.T) {
	metrics := Simulate()
	if len(metrics) != 55 {
		t.Fatalf("got %d scenarios/strategies", len(metrics))
	}
	for _, m := range metrics {
		if m.Scenario == "all-slow" && (m.Strategy == player.ClockBounded || m.Strategy == player.ClockInterval) {
			if m.Samples != 0 || m.Rejected != 70 {
				t.Fatalf("all-rejected strategy presented as measured: %+v", m)
			}
			continue
		}
		if m.Samples < 6000 || math.IsNaN(m.MeanMS) || math.IsInf(m.MeanMS, 0) {
			t.Fatalf("unusable simulation score: %+v", m)
		}
		if m.Scenario == "constant-high-rtt" && m.P95MS < 40 {
			t.Fatalf("asymmetric timing uncertainty was hidden: %+v", m)
		}
		if m.Scenario == "slow-outliers" && m.Strategy == player.ClockInterval && (m.MaxMS > 10 || m.Rejected != 6) {
			t.Fatalf("outlier rejection regressed: %+v", m)
		}
		if m.Scenario == "seek-pause-repeat" && m.MaxMS < 20000 {
			t.Fatalf("poll-only transition detection latency was hidden: %+v", m)
		}
	}
}
