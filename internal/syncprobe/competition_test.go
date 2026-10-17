package syncprobe

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestQueryAtIsDeterministicForSeedAndRequest(t *testing.T) {
	scenario := competitionScenario{name: "jitter", jitter: .03}
	first := queryAt(12.345, scenario, 17, 4)
	repeat := queryAt(12.345, scenario, 17, 4)
	if !reflect.DeepEqual(first, repeat) {
		t.Fatalf("same seed and request produced different replies:\nfirst: %#v\nrepeat: %#v", first, repeat)
	}
	otherSeed := queryAt(12.345, scenario, 18, 4)
	if reflect.DeepEqual(first, otherSeed) {
		t.Fatalf("different seeds produced identical query replies: %#v", first)
	}
}

func TestQueryAtReplyIsCausalAndKeepsRequestGeneration(t *testing.T) {
	const requestStart = 8.25
	const generation = 23
	reply := queryAt(requestStart, competitionScenario{name: "fresh"}, 31, generation)
	base := time.Unix(1700000000, 0)
	requestAt := base.Add(time.Duration(requestStart * float64(time.Second)))
	truthAtRequest := competitionTruth(requestStart, competitionScenario{name: "fresh"})
	truthAtReply := competitionTruth(reply.due, competitionScenario{name: "fresh"})
	if reply.failed {
		t.Fatal("fresh query unexpectedly failed")
	}
	if reply.due < requestStart || reply.sample.StartedAt.Before(requestAt) || reply.sample.FinishedAt.Before(reply.sample.StartedAt) {
		t.Fatalf("reply timestamps precede the request: start=%v finish=%v due=%.6f", reply.sample.StartedAt, reply.sample.FinishedAt, reply.due)
	}
	if !reply.sample.FinishedAt.Equal(base.Add(time.Duration(reply.due * float64(time.Second)))) {
		t.Fatalf("reply finish %v does not match due time %.6f", reply.sample.FinishedAt, reply.due)
	}
	if reply.sample.Generation != generation {
		t.Fatalf("reply generation = %d, want request generation %d", reply.sample.Generation, generation)
	}
	// With a fresh, noiseless player, MPRIS may sample at any time within the
	// request interval. Its returned position cannot encode a later state.
	if reply.sample.Position < truthAtRequest.position || reply.sample.Position > truthAtReply.position {
		t.Fatalf("position %.6f falls outside causal request interval [%.6f, %.6f]", reply.sample.Position, truthAtRequest.position, truthAtReply.position)
	}
}

func TestTransportSignalPositionDoesNotUseTruthForPauseResumeOrRate(t *testing.T) {
	for _, test := range []struct {
		scenario competitionScenario
		index    int
	}{
		{competitionScenario{transports: true}, 1},
		{competitionScenario{transports: true}, 2},
		{competitionScenario{rateChange: true}, 0},
		{competitionScenario{rateChange: true}, 1},
	} {
		// Deliberately give the candidate a badly wrong estimate. A state-only
		// signal must not magically repair it with the simulator's true position.
		arrival := competitionActions(test.scenario)[test.index] + .02
		event := transportSample(test.scenario, test.index, arrival, 7, 123.456)
		if event.Position != 123.456 || event.Generation != 7 {
			t.Fatalf("signal leaked position truth: %+v", event)
		}
		if event.StartedAt != event.FinishedAt {
			t.Fatalf("signal contains a hidden sample timestamp: %+v", event)
		}
	}
	seek := transportSample(competitionScenario{transports: true}, 0, 12.39, 8, 123.456)
	if seek.Position != 4 {
		t.Fatalf("Seeked lost its real position payload: %+v", seek)
	}
}

func TestCompetitionPollingSchedulesRespectSharedQueryBudget(t *testing.T) {
	scenario := competitionScenario{name: "fresh"}
	for _, candidate := range competitors {
		first := runCompetition(scenario, 12, candidate)
		repeat := runCompetition(scenario, 12, candidate)
		if first.queries != repeat.queries {
			t.Errorf("%s query count is nondeterministic: %d then %d", candidate.name, first.queries, repeat.queries)
		}
		// The token bucket starts with one token and refills at one token per
		// second over a 90-second run, regardless of the requested poll cadence.
		if first.queries < 1 || first.queries > 91 {
			t.Errorf("%s issued %d queries; shared 90-second budget allows at most 91", candidate.name, first.queries)
		}
	}
}

func TestCompetitionScoresPausedAndTransitionTimesteps(t *testing.T) {
	run := runCompetition(
		competitionScenario{name: "seek-pause-repeat", transports: true},
		7,
		competitor{name: "periodic", method: "periodic"},
	)
	const expectedScoredSteps = 89 * 100
	if run.scored != expectedScoredSteps {
		t.Fatalf("scored %d time steps, want all %d post-startup steps", run.scored, expectedScoredSteps)
	}
	if len(run.errors)+run.missing != run.scored {
		t.Fatalf("scored steps are not accounted for: errors=%d missing=%d scored=%d", len(run.errors), run.missing, run.scored)
	}
	if math.IsNaN(run.recovery) || math.IsInf(run.recovery, 0) {
		t.Fatalf("recovery score is not finite: %v", run.recovery)
	}
}
