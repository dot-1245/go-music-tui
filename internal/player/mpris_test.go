package player

import (
	"math"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func variant(value any) dbus.Variant { return dbus.MakeVariant(value) }

func baseMPRISState(now time.Time) mprisState {
	return mprisState{
		ready: true,
		snapshot: Snapshot{ReceivedAt: now, Info: Info{
			TrackID: "/track/one", Title: "Song", Artist: "Artist", Album: "Album",
			Status: "Playing", PositionSeconds: 10, Position: 10, LengthSeconds: 100, Rate: 1,
		}},
	}
}

func TestMPRISMetadataDecode(t *testing.T) {
	metadata := map[string]dbus.Variant{
		"mpris:trackid": variant(dbus.ObjectPath("/track/42")),
		"xesam:title":   variant("Title"),
		"xesam:album":   variant("Album"),
		"xesam:artist":  variant([]string{"First", "Second"}),
		"mpris:artUrl":  variant("file:///cover.jpg"),
		"mpris:length":  variant(int64(12345678)),
	}
	info := applyMPRISProperties(Info{}, map[string]dbus.Variant{
		"Metadata": variant(metadata), "PlaybackStatus": variant("Playing"),
		"Rate": variant(float64(1.5)), "Volume": variant(float64(.42)),
		"Shuffle": variant(true), "LoopStatus": variant("Playlist"),
	})
	if info.TrackID != "/track/42" || info.Title != "Title" || info.Artist != "First, Second" || len(info.Artists) != 2 || info.LengthSeconds != 12.345678 || info.Rate != 1.5 || info.Volume != 42 || info.Shuffle != "true" || info.Loop != "Playlist" {
		t.Fatalf("decoded info = %#v", info)
	}
	info = applyMPRISProperties(Info{}, map[string]dbus.Variant{"Metadata": variant(map[string]dbus.Variant{"xesam:artist": variant("A; B")})})
	if info.Artist != "A, B" || len(info.Artists) != 2 {
		t.Fatalf("string artist fallback = %#v", info)
	}
}

func TestMPRISStateTrackSwitchSameMetadata(t *testing.T) {
	now := time.Now()
	state := baseMPRISState(now)
	emit, query := state.properties(map[string]dbus.Variant{"Metadata": variant(map[string]dbus.Variant{"mpris:trackid": variant(dbus.ObjectPath("/track/two")), "xesam:title": variant("Song"), "xesam:artist": variant("Artist"), "xesam:album": variant("Album")})}, nil, now.Add(time.Second))
	if !emit || !query || state.snapshot.Info.TrackID != "/track/two" || state.snapshot.Info.PositionSeconds != 0 {
		t.Fatalf("track switch: emit=%v query=%v snapshot=%+v", emit, query, state.snapshot)
	}
}

func TestMPRISStateTransportAnchorAndInvalidation(t *testing.T) {
	now := time.Now()
	state := baseMPRISState(now)
	changedAt := now.Add(4 * time.Second)
	emit, query := state.properties(map[string]dbus.Variant{"PlaybackStatus": variant("Paused")}, nil, changedAt)
	if !emit || !query || state.snapshot.Info.PositionSeconds != 14 || state.snapshot.Info.Status != "Paused" || !state.snapshot.ReceivedAt.Equal(changedAt) {
		t.Fatalf("pause anchor: emit=%v query=%v snapshot=%+v", emit, query, state.snapshot)
	}
	state.snapshot.Info.Status = "Playing"
	state.snapshot.ReceivedAt = changedAt
	emit, query = state.properties(map[string]dbus.Variant{"Rate": variant(float64(1.5))}, nil, changedAt.Add(2*time.Second))
	if !emit || !query || state.snapshot.Info.PositionSeconds != 16 || state.snapshot.Info.Rate != 1.5 {
		t.Fatalf("rate anchor: emit=%v query=%v snapshot=%+v", emit, query, state.snapshot)
	}
	before := state.generation
	emit, query = state.properties(nil, []string{"Metadata", "Position"}, changedAt.Add(3*time.Second))
	if emit || !query || state.generation != before+2 {
		t.Fatalf("invalidations: emit=%v query=%v generation=%d (was %d)", emit, query, state.generation, before)
	}
}

func TestMPRISStateSeekAndStaleReply(t *testing.T) {
	now := time.Now()
	state := baseMPRISState(now)
	oldGeneration := state.generation
	if !state.seek(25_500_000, now.Add(time.Second)) || state.snapshot.Info.PositionSeconds != 25.5 || state.snapshot.Info.Position != 25 {
		t.Fatalf("seek state = %+v", state.snapshot)
	}
	stale := mprisReply{generation: oldGeneration, snapshot: Snapshot{Info: Info{Title: "stale"}}}
	if state.accept(stale) || state.snapshot.Info.Title != "Song" {
		t.Fatal("accepted stale query reply")
	}
	if state.seek(-1, now) {
		t.Fatal("accepted negative seek")
	}
}

func TestMPRISSnapshotRateAdvancesPosition(t *testing.T) {
	for _, rate := range []float64{.5, 1.5, -1} {
		t.Run(map[float64]string{.5: "half", 1.5: "one-and-half", -1: "reverse"}[rate], func(t *testing.T) {
			snapshot := Snapshot{ReceivedAt: time.Unix(1, 0), Info: Info{Status: "Playing", PositionSeconds: 10, LengthSeconds: 100, Rate: rate}}
			want := 10 + rate*2
			if got := snapshot.PositionAt(time.Unix(3, 0)); math.Abs(got-want) > 1e-9 {
				t.Fatalf("PositionAt = %v, want %v", got, want)
			}
		})
	}
}
