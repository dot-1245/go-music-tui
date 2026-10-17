package player

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type rawPositionRunner struct {
	metadata []string
	position string
	args     [][]string
}

func (r *rawPositionRunner) Output(_ context.Context, args ...string) ([]byte, error) {
	r.args = append(r.args, append([]string(nil), args...))
	if len(args) >= 4 && args[2] == "metadata" && args[3] == "--format" {
		if len(r.metadata) == 0 {
			return nil, fmt.Errorf("unexpected metadata request")
		}
		value := r.metadata[0]
		r.metadata = r.metadata[1:]
		return []byte(value), nil
	}
	if len(args) == 3 && args[2] == "position" {
		return []byte(r.position), nil
	}
	return nil, fmt.Errorf("unexpected playerctl arguments: %q", args)
}

func (*rawPositionRunner) Run(context.Context, ...string) error { return nil }

func rawPositionMetadata(status, title string) string {
	return strings.Join([]string{"99000000", "240000000", "1", status, title, "Artist", "Album", "", "false", "None", "mpv"}, metadataFieldSeparator)
}

func TestObserveRawPositionUsesExplicitPositionCommand(t *testing.T) {
	runner := &rawPositionRunner{
		metadata: []string{rawPositionMetadata("Playing", "Track"), rawPositionMetadata("Playing", "Track")},
		position: " 7.25\n",
	}
	observation, err := New(runner).ObserveRawPosition(context.Background(), "mpv")
	if err != nil {
		t.Fatalf("ObserveRawPosition returned error: %v", err)
	}
	if got := observation.Snapshot.Info.PositionSeconds; got != 7.25 {
		t.Fatalf("raw position = %v, want 7.25 (metadata format position was 99)", got)
	}
	if observation.Snapshot.Info.Position != 7 {
		t.Fatalf("integer position = %d, want 7", observation.Snapshot.Info.Position)
	}
	if len(runner.args) != 3 || strings.Join(runner.args[1], " ") != "-p mpv position" {
		t.Fatalf("calls = %q, want metadata, explicit '-p mpv position', metadata", runner.args)
	}
	if observation.StartedAt.IsZero() || observation.FinishedAt.IsZero() || observation.FinishedAt.Before(observation.StartedAt) {
		t.Fatalf("observation interval is reversed: %v .. %v", observation.StartedAt, observation.FinishedAt)
	}
}

func TestObserveRawPositionRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"not-a-number", "NaN", "+Inf", "-Inf", "-0.01", ""} {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			runner := &rawPositionRunner{
				metadata: []string{rawPositionMetadata("Playing", "Track"), rawPositionMetadata("Playing", "Track")},
				position: value,
			}
			if _, err := New(runner).ObserveRawPosition(context.Background(), "mpv"); err == nil {
				t.Fatalf("ObserveRawPosition accepted %q", value)
			}
		})
	}
}

func TestObserveRawPositionRejectsTrackOrStatusChanges(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metadata []string
	}{
		{name: "track", metadata: []string{rawPositionMetadata("Playing", "Before"), rawPositionMetadata("Playing", "After")}},
		{name: "status", metadata: []string{rawPositionMetadata("Playing", "Track"), rawPositionMetadata("Paused", "Track")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &rawPositionRunner{metadata: tc.metadata, position: "12.5"}
			if _, err := New(runner).ObserveRawPosition(context.Background(), "mpv"); err == nil {
				t.Fatal("ObserveRawPosition accepted metadata that changed during the position query")
			}
		})
	}
}
