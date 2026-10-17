package syncprobe

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/dot-1245/go-music-tui/internal/player"
)

const competitionSeconds = 90.0

type CompetitionRow struct {
	Split            string  `json:"split"`
	Scenario         string  `json:"scenario"`
	Method           string  `json:"method"`
	P95MS            float64 `json:"p95_ms"`
	P99MS            float64 `json:"p99_ms"`
	MaxMS            float64 `json:"max_ms"`
	Over50Percent    float64 `json:"over_50ms_percent"`
	CoveragePercent  float64 `json:"coverage_percent"`
	RecoveryMS       float64 `json:"worst_recovery_ms"`
	RecoveryFailures int     `json:"recovery_failures"`
	Backsteps        int     `json:"backsteps"`
	QueriesPerMinute float64 `json:"queries_per_minute"`
}

type CompetitionVerdict struct {
	Split               string   `json:"split"`
	Method              string   `json:"method"`
	Survives            bool     `json:"survives"`
	Reasons             []string `json:"reasons"`
	ImprovedScenarios   int      `json:"improved_scenarios"`
	SavedQueriesPercent float64  `json:"saved_queries_percent"`
}

type CompetitionReport struct {
	DevelopmentSeeds []int64              `json:"development_seeds"`
	ValidationSeeds  []int64              `json:"validation_seeds"`
	Rows             []CompetitionRow     `json:"rows"`
	Verdicts         []CompetitionVerdict `json:"verdicts"`
	Survivors        []string             `json:"survivors"`
}

type competitor struct {
	name     string
	method   player.CompetitionMethod
	adaptive bool
}

var competitors = []competitor{
	{"periodic", player.SyncPeriodic, false},
	{"gentle", player.SyncGentle, false},
	{"median-3", player.SyncMedian, false},
	{"pll", player.SyncPLL, false},
	{"adaptive-periodic", player.SyncPeriodic, true},
	{"adaptive-gentle", player.SyncGentle, true},
}

type competitionScenario struct {
	name                                                            string
	jitter, cache, quantum                                          float64
	slow, outlier, transports, missing, dropout, freeze, rateChange bool
}

var competitionScenarios = []competitionScenario{
	{name: "fresh"},
	{name: "jitter", jitter: .03},
	{name: "slow-responses", slow: true},
	{name: "position-outlier", outlier: true},
	{name: "cached-250ms", cache: .25},
	{name: "cached-800ms", cache: .8},
	{name: "rounded-position", quantum: 1},
	{name: "seek-pause-repeat", transports: true},
	{name: "missing-signals", transports: true, missing: true},
	{name: "query-timeouts", dropout: true, jitter: .01},
	{name: "frozen-position", freeze: true},
	{name: "rate-changes", rateChange: true},
}

type competitionState struct {
	position, rate float64
	track, status  string
}

func competitionTruth(t float64, s competitionScenario) competitionState {
	w := competitionState{position: 10 + t, rate: 1, track: "track-1", status: "Playing"}
	if s.rateChange {
		if t >= 60.37 {
			w.position = 85.37 + (t-60.37)*.5
			w.rate = .5
		} else if t >= 30.37 {
			w.position = 40.37 + (t-30.37)*1.5
			w.rate = 1.5
		}
	}
	if s.transports {
		x := truth(t, true, 1)
		w.position = x.position
		w.track = x.title
		w.status = x.status
	}
	return w
}

func competitionActions(s competitionScenario) []float64 {
	if s.transports {
		return []float64{12.37, 24.37, 27.37, 40.37, 55.37}
	}
	if s.rateChange {
		return []float64{30.37, 60.37}
	}
	return nil
}

func transportSample(s competitionScenario, index int, arrival float64, generation uint64, estimatedPosition float64) player.PositionSample {
	event := positionSample(competitionTruth(competitionActions(s)[index], s), arrival, arrival, generation)
	// Status/Rate signals contain no Position. Only Seeked carries position;
	// a normal track change starts at zero under the MPRIS contract.
	if s.rateChange || s.transports && (index == 1 || index == 2) {
		event.Position = estimatedPosition
	}
	return event
}

func positionSample(w competitionState, start, end float64, generation uint64) player.PositionSample {
	base := time.Unix(1700000000, 0)
	return player.PositionSample{Position: math.Max(0, w.position), Rate: w.rate, Track: w.track, Status: w.status, StartedAt: base.Add(time.Duration(start * 1e9)), FinishedAt: base.Add(time.Duration(end * 1e9)), Generation: generation}
}

type queryReply struct {
	sample player.PositionSample
	due    float64
	failed bool
}

// queryAt is a deterministic source: matching request times see matching data,
// independent of the estimator. Cached positions can predate the entire query.
func queryAt(start float64, s competitionScenario, seed int64, generation uint64) queryReply {
	rng := rand.New(rand.NewSource(seed*7919 + int64(math.Round(start*100))*104729))
	rtt := .004 + rng.Float64()*.016
	if s.slow && rng.Float64() < .12 {
		rtt = .65
	}
	fraction := rng.Float64()
	sampled := start + rtt*fraction
	w := competitionTruth(sampled, s)
	if s.cache > 0 {
		w.position = competitionTruth(math.Floor(sampled/s.cache)*s.cache, s).position
	}
	if s.freeze && sampled >= 25.37 && sampled < 30.37 {
		w.position = competitionTruth(25.37, s).position
	}
	if s.quantum > 0 {
		w.position = math.Floor(w.position/s.quantum) * s.quantum
	}
	w.position += (rng.Float64()*2 - 1) * s.jitter
	if s.outlier && rng.Float64() < .1 {
		w.position += .8
	}
	failed := s.dropout && rng.Float64() < .15
	if failed {
		rtt = .8
	}
	return queryReply{sample: positionSample(w, start, start+rtt, generation), due: start + rtt, failed: failed}
}

type competitionRun struct {
	errors                                      []float64
	scored, missing, over50, queries, backsteps int
	recovery                                    float64
	recoveryFailures                            int
}

func runCompetition(s competitionScenario, seed int64, candidate competitor) competitionRun {
	clock := player.CompetitionClock{Method: candidate.method}
	var result competitionRun
	var pending *queryReply
	// Vary poll phase: integer-second alignment can bias cache/rounding tests.
	queryAtTime := .05 + rand.New(rand.NewSource(seed*31337)).Float64()*.8
	tokens := 1.0
	stable := 0
	generation := uint64(0)
	actions := competitionActions(s)
	truthAction, eventAction := 0, 0
	recovering := false
	recoveryStart, goodSince, lastAction := 0.0, -1.0, math.Inf(-1)
	previous := math.NaN()
	for step := 0; step < int(competitionSeconds*100); step++ {
		t := float64(step) / 100
		now := time.Unix(1700000000, 0).Add(time.Duration(step) * 10 * time.Millisecond)
		tokens = math.Min(2, tokens+.01)
		if truthAction < len(actions) && t >= actions[truthAction] {
			if recovering {
				result.recoveryFailures++
				result.recovery = math.Max(result.recovery, t-recoveryStart)
			}
			recovering = true
			recoveryStart = actions[truthAction]
			goodSince = -1
			lastAction = recoveryStart
			truthAction++
		}
		// Every candidate gets identical transport signals, with the same
		// delivery delay. Missing signals are not reconstructed from truth.
		if eventAction < len(actions) && t >= actions[eventAction]+.02 {
			if !s.missing || eventAction%2 == 1 {
				generation++
				event := transportSample(s, eventAction, t, generation, clock.PositionAt(now))
				clock.Transport(event)
				if candidate.adaptive {
					queryAtTime = t
					stable = 0
				}
			}
			eventAction++
		}
		if pending != nil && pending.due <= t {
			if !pending.failed && pending.sample.Generation == generation {
				predicted := clock.PositionAt(pending.sample.FinishedAt)
				residual := math.Abs(predicted - pending.sample.Position)
				accepted := clock.Observe(pending.sample)
				if candidate.adaptive && accepted {
					if !math.IsNaN(residual) && residual < .02 {
						stable++
					} else {
						stable = 0
					}
					interval := 1.0
					if stable >= 5 {
						interval = 2
					}
					if !math.IsNaN(residual) && residual > .05 {
						interval = .25
					}
					queryAtTime = t + interval
				}
			}
			pending = nil
		}
		if pending == nil && t >= queryAtTime && tokens >= 1 {
			query := queryAt(t, s, seed, generation)
			pending = &query
			result.queries++
			tokens--
			queryAtTime = t + 1
		}
		position := clock.PositionAt(now)
		w := competitionTruth(t, s)
		// A common fixed 1s startup window is excluded for everyone. Pauses,
		// all transitions and query failures after that are included.
		if t >= 1 {
			result.scored++
			if math.IsNaN(position) {
				result.missing++
			} else {
				error := math.Abs(position - w.position)
				result.errors = append(result.errors, error)
				if error > .05 {
					result.over50++
				}
				if t-lastAction > 2 && !math.IsNaN(previous) && position < previous-1e-6 && w.status == "Playing" {
					result.backsteps++
				}
				if recovering {
					if error <= .05 {
						if goodSince < 0 {
							goodSince = t
						}
						if t-goodSince >= .1 {
							result.recovery = math.Max(result.recovery, goodSince-recoveryStart)
							recovering = false
						}
					} else {
						goodSince = -1
					}
				}
			}
		}
		previous = position
	}
	if recovering {
		result.recoveryFailures++
		result.recovery = math.Max(result.recovery, competitionSeconds-recoveryStart)
	}
	return result
}

func competitionRow(split string, s competitionScenario, candidate competitor, seeds []int64) CompetitionRow {
	row := CompetitionRow{Split: split, Scenario: s.name, Method: candidate.name}
	var errors []float64
	total, missing, over50, queries := 0, 0, 0, 0
	for _, seed := range seeds {
		r := runCompetition(s, seed, candidate)
		errors = append(errors, r.errors...)
		total += r.scored
		missing += r.missing
		over50 += r.over50
		queries += r.queries
		row.Backsteps += r.backsteps
		row.RecoveryMS = math.Max(row.RecoveryMS, r.recovery*1000)
		row.RecoveryFailures += r.recoveryFailures
	}
	sort.Float64s(errors)
	if len(errors) > 0 {
		row.P95MS = errors[int(math.Ceil(float64(len(errors))*.95))-1] * 1000
		row.P99MS = errors[int(math.Ceil(float64(len(errors))*.99))-1] * 1000
		row.MaxMS = errors[len(errors)-1] * 1000
	}
	row.CoveragePercent = float64(total-missing) / float64(total) * 100
	row.Over50Percent = float64(over50) / float64(total) * 100
	row.QueriesPerMinute = float64(queries) / float64(len(seeds)) / competitionSeconds * 60
	return row
}

// Tournament applies fixed gates to both discovery and disjoint validation
// seeds. All comparisons are per scenario, never just an overall average.
func Tournament() CompetitionReport {
	report := CompetitionReport{DevelopmentSeeds: []int64{1, 2, 3, 4, 5}, ValidationSeeds: []int64{101, 102, 103, 104, 105}}
	for _, split := range []string{"development", "validation"} {
		seeds := report.DevelopmentSeeds
		if split == "validation" {
			seeds = report.ValidationSeeds
		}
		for _, s := range competitionScenarios {
			for _, c := range competitors {
				report.Rows = append(report.Rows, competitionRow(split, s, c, seeds))
			}
		}
		for _, candidate := range competitors[1:] {
			verdict := CompetitionVerdict{Split: split, Method: candidate.name, Survives: true}
			baseQueries, candidateQueries := 0.0, 0.0
			for _, s := range competitionScenarios {
				var baseline, row CompetitionRow
				for _, r := range report.Rows {
					if r.Split == split && r.Scenario == s.name {
						if r.Method == "periodic" {
							baseline = r
						}
						if r.Method == candidate.name {
							row = r
						}
					}
				}
				baseQueries += baseline.QueriesPerMinute
				candidateQueries += row.QueriesPerMinute
				reason := ""
				switch {
				case row.CoveragePercent < baseline.CoveragePercent-.01:
					reason = "coverage"
				case row.RecoveryFailures > baseline.RecoveryFailures:
					reason = "failed recovery"
				case row.RecoveryMS > baseline.RecoveryMS+100:
					reason = "recovery > baseline+100ms"
				case row.Backsteps > baseline.Backsteps+5:
					reason = "backsteps > baseline+5"
				case row.P95MS > baseline.P95MS+5:
					reason = "p95 > baseline+5ms"
				case row.P99MS > baseline.P99MS+25:
					reason = "p99 > baseline+25ms"
				case row.MaxMS > baseline.MaxMS+100:
					reason = "max > baseline+100ms"
				case row.Over50Percent > baseline.Over50Percent+.5:
					reason = ">50ms duration > baseline+0.5pp"
				case row.QueriesPerMinute > baseline.QueriesPerMinute+1:
					reason = "query budget"
				}
				if reason != "" {
					verdict.Survives = false
					verdict.Reasons = append(verdict.Reasons, s.name+": "+reason)
				}
				if row.P95MS < baseline.P95MS-5 {
					verdict.ImprovedScenarios++
				}
			}
			verdict.SavedQueriesPercent = (1 - candidateQueries/baseQueries) * 100
			if verdict.ImprovedScenarios == 0 && verdict.SavedQueriesPercent < 10 {
				verdict.Survives = false
				verdict.Reasons = append(verdict.Reasons, "no material accuracy gain or >=10% query saving")
			}
			report.Verdicts = append(report.Verdicts, verdict)
		}
	}
	report.Survivors = []string{"periodic"}
	for _, candidate := range competitors[1:] {
		passed := 0
		for _, verdict := range report.Verdicts {
			if verdict.Method == candidate.name && verdict.Survives {
				passed++
			}
		}
		if passed == 2 {
			report.Survivors = append(report.Survivors, candidate.name)
		}
	}
	return report
}

// CompetitionReplay measures next-query consistency, not audible position.
// Adaptive scheduling is evaluated in simulation only. Old traces assume rate 1.
func CompetitionReplay(observations []player.Observation, stride int) []Metrics {
	if stride < 1 {
		stride = 1
	}
	var metrics []Metrics
	for _, candidate := range competitors[:4] {
		clock := player.CompetitionClock{Method: candidate.method}
		var errors, outside []float64
		rejected := 0
		for i, o := range observations {
			predicted := clock.PositionAt(o.FinishedAt)
			if i > 0 && !math.IsNaN(predicted) && o.Snapshot.Info.Status == "Playing" && observations[i-1].Snapshot.Info.Status == "Playing" && observations[i-1].Snapshot.TrackKey() == o.Snapshot.TrackKey() {
				errors = append(errors, math.Abs(predicted-o.Snapshot.Info.PositionSeconds))
				outside = append(outside, outsideInterval(predicted, o))
			}
			if i%stride == 0 {
				sample := player.PositionSample{Position: o.Snapshot.Info.PositionSeconds, Rate: 1, Track: o.Snapshot.TrackKey(), Status: o.Snapshot.Info.Status, StartedAt: o.StartedAt, FinishedAt: o.FinishedAt}
				if !clock.Observe(sample) {
					rejected++
				}
			}
		}
		m := summarize("live-consistency-not-truth", player.ClockStrategy(candidate.name), errors, 0, rejected)
		m.OutsideP95MS = summarize("", "", outside, 0, 0).P95MS
		metrics = append(metrics, m)
	}
	return metrics
}
