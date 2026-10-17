// Package syncprobe compares clock strategies against deterministic playback
// traces. Synthetic truth is kept separate from the observations given to clocks.
package syncprobe

import (
	"math"
	"sort"
	"time"

	"github.com/dot-1245/go-music-tui/internal/player"
)

var Strategies = []player.ClockStrategy{player.ClockReceipt, player.ClockMidpoint, player.ClockSlew, player.ClockBounded, player.ClockInterval}

type Metrics struct {
	Scenario     string               `json:"scenario"`
	Strategy     player.ClockStrategy `json:"strategy"`
	MeanMS       float64              `json:"mean_ms"`
	P95MS        float64              `json:"p95_ms"`
	MaxMS        float64              `json:"max_ms"`
	Backsteps    int                  `json:"backsteps"`
	Rejected     int                  `json:"rejected"`
	Samples      int                  `json:"samples"`
	OutsideP95MS float64              `json:"outside_interval_p95_ms"`
}

func summarize(scenario string, strategy player.ClockStrategy, errors []float64, backsteps, rejected int) Metrics {
	m := Metrics{Scenario: scenario, Strategy: strategy, Backsteps: backsteps, Rejected: rejected, Samples: len(errors)}
	if len(errors) == 0 {
		return m
	}
	for _, e := range errors {
		m.MeanMS += e * 1000
	}
	m.MeanMS /= float64(len(errors))
	sort.Float64s(errors)
	m.P95MS = errors[int(math.Ceil(float64(len(errors))*0.95))-1] * 1000
	m.MaxMS = errors[len(errors)-1] * 1000
	return m
}

type world struct {
	position      float64
	title, status string
}

func truth(t float64, transitions bool, speed float64) world {
	w := world{position: 10 + t*speed, title: "track-1", status: "Playing"}
	if !transitions {
		return w
	}
	switch {
	case t >= 55.37:
		w.title = "track-2"
		w.position = (t - 55.37) * speed
	case t >= 40.37:
		w.title = "track-2"
		w.position = (t - 40.37) * speed
	case t >= 27.37:
		w.position = 16 + (t-27.37)*speed
	case t >= 24.37:
		w.position = 16
		w.status = "Paused"
	case t >= 12.37:
		w.position = 4 + (t-12.37)*speed
	}
	return w
}

// Simulate runs fixed, reproducible scenarios. Queries are one second apart;
// errors include time before a seek/pause is observed (not hidden as warm-up).
func Simulate() []Metrics {
	type scenario struct {
		name                string
		fraction, speed     float64
		stalls, transitions bool
	}
	cases := []scenario{
		{"symmetric", .5, 1, false, false},
		{"sample-early", .1, 1, false, false},
		{"sample-late", .9, 1, false, false},
		{"slow-outliers", .1, 1, true, false},
		{"rate-drift", .5, 1.005, false, false},
		{"seek-pause-repeat", .5, 1, true, true},
		{"constant-high-rtt", .1, 1, false, false},
		{"changing-asymmetry", .5, 1, false, false},
		{"coarse-position", .5, 1, false, false},
		{"rate-slow", .5, .995, false, false},
		{"all-slow", .5, 1, false, false},
	}
	base := time.Unix(1700000000, 0)
	var results []Metrics
	for _, s := range cases {
		var observations []player.Observation
		for i := 0; i < 70; i++ {
			// Avoid aligning sampling with integer-second quantization or the
			// scripted transport transitions; that would give misleading scores.
			start := float64(i) + .173
			rtt := []float64{.008, .02, .06, .012, .04}[i%5]
			fraction := s.fraction
			quantum := .001
			switch s.name {
			case "all-slow":
				rtt = .65
			case "constant-high-rtt":
				rtt = .12
			case "changing-asymmetry":
				fraction = []float64{.1, .9, .5, .8, .2}[i%5]
			case "coarse-position":
				quantum = 1
			}
			if s.stalls && i%11 == 5 {
				rtt = .65
			}
			w := truth(start+rtt*fraction, s.transitions, s.speed)
			info := player.Info{Name: "simulation", Title: w.title, Status: w.status, PositionSeconds: math.Floor(w.position/quantum) * quantum, LengthSeconds: 300}
			observations = append(observations, player.Observation{Snapshot: player.Snapshot{Info: info}, StartedAt: base.Add(time.Duration(start * 1e9)), FinishedAt: base.Add(time.Duration((start + rtt) * 1e9))})
		}
		for _, strategy := range Strategies {
			clock := player.PlaybackClock{Strategy: strategy}
			var errors []float64
			next, rejected, backsteps := 0, 0, 0
			previous := math.NaN()
			previousTruth := world{}
			lastTransportAt := math.Inf(-1)
			for step := 0; step < 7000; step++ {
				t := float64(step) / 100
				now := base.Add(time.Duration(step) * 10 * time.Millisecond)
				for next < len(observations) && !observations[next].FinishedAt.After(now) {
					if !clock.Observe(observations[next]) {
						rejected++
					}
					next++
				}
				position := clock.PositionAt(now)
				w := truth(t, s.transitions, s.speed)
				if step > 0 && (w.title != previousTruth.title || w.status != previousTruth.status || w.position < previousTruth.position) {
					lastTransportAt = t
				}
				if !math.IsNaN(position) {
					errors = append(errors, math.Abs(position-w.position))
					if !math.IsNaN(previous) && position < previous-1e-6 && t-lastTransportAt > 2 && w.title == previousTruth.title && w.status == "Playing" && previousTruth.status == "Playing" && w.position >= previousTruth.position {
						backsteps++
					}
				}
				previous, previousTruth = position, w
			}
			results = append(results, summarize(s.name, strategy, errors, backsteps, rejected))
		}
	}
	return results
}

// Disagreement compares prediction just before observing a new query with that
// query's position projected to receipt time. This is consistency, NOT audio
// ground truth. A low value can also mean consistently biased observations.
func Disagreement(observations []player.Observation) []Metrics {
	return DisagreementEvery(observations, 1)
}

// DisagreementEvery feeds only every stride-th query to each estimator, while
// scoring against all queries. This checks a lower polling budget without
// making the estimator see intermediate validation samples.
func DisagreementEvery(observations []player.Observation, stride int) []Metrics {
	if stride < 1 {
		stride = 1
	}
	var results []Metrics
	for _, strategy := range Strategies {
		clock := player.PlaybackClock{Strategy: strategy}
		var errors []float64
		var outside []float64
		rejected := 0
		var previous *player.Observation
		for i := range observations {
			o := observations[i]
			predicted := clock.PositionAt(o.FinishedAt)
			if previous != nil && o.Snapshot.Info.Status == "Playing" && previous.Snapshot.TrackKey() == o.Snapshot.TrackKey() && previous.Snapshot.Info.Status == o.Snapshot.Info.Status && !math.IsNaN(predicted) {
				measured := o.Snapshot
				measured.ReceivedAt = o.StartedAt.Add(o.FinishedAt.Sub(o.StartedAt) / 2)
				errors = append(errors, math.Abs(predicted-measured.PositionAt(o.FinishedAt)))
				outside = append(outside, outsideInterval(predicted, o))
			}
			if i%stride == 0 && !clock.Observe(o) {
				rejected++
			}
			previous = &observations[i]
		}
		metric := summarize("live-consistency-not-truth", strategy, errors, 0, rejected)
		metric.OutsideP95MS = summarize("", strategy, outside, 0, 0).P95MS
		results = append(results, metric)
	}
	return results
}

// FollowDisagreement evaluates the actual production follow stream against the
// same query observations. It never extrapolates a future notification backwards.
func FollowDisagreement(observations []player.Observation, snapshots []player.Snapshot) Metrics {
	var errors []float64
	var outside []float64
	next := 0
	var current *player.Snapshot
	for i, o := range observations {
		for next < len(snapshots) && !snapshots[next].ReceivedAt.After(o.FinishedAt) {
			current = &snapshots[next]
			next++
		}
		if i == 0 || observations[i-1].Snapshot.Info.Status != o.Snapshot.Info.Status || observations[i-1].Snapshot.TrackKey() != o.Snapshot.TrackKey() || current == nil || o.Snapshot.Info.Status != "Playing" || current.TrackKey() != o.Snapshot.TrackKey() || current.Info.Status != o.Snapshot.Info.Status {
			continue
		}
		measured := o.Snapshot
		measured.ReceivedAt = o.StartedAt.Add(o.FinishedAt.Sub(o.StartedAt) / 2)
		errors = append(errors, math.Abs(current.PositionAt(o.FinishedAt)-measured.PositionAt(o.FinishedAt)))
		outside = append(outside, outsideInterval(current.PositionAt(o.FinishedAt), o))
	}
	metric := summarize("live-consistency-not-truth", player.ClockStrategy("actual-follow"), errors, 0, 0)
	metric.OutsideP95MS = summarize("", "", outside, 0, 0).P95MS
	return metric
}

func outsideInterval(position float64, o player.Observation) float64 {
	lower := o.Snapshot.Info.PositionSeconds
	upper := lower + o.FinishedAt.Sub(o.StartedAt).Seconds()
	return math.Max(0, math.Max(lower-position, position-upper))
}
