package player

import (
	"context"
	"math"
	"os"
	"runtime"
	"testing"
	"time"
)

type fakeRunner struct {
	outputs map[string]string
	args    [][]string
}

type blockingRunner struct{}

func (blockingRunner) Output(ctx context.Context, _ ...string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingRunner) Run(ctx context.Context, _ ...string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeRunner) Output(_ context.Context, args ...string) ([]byte, error) {
	key := ""
	for _, arg := range args {
		key += "\x00" + arg
	}
	return []byte(f.outputs[key]), nil
}

func (f *fakeRunner) Run(_ context.Context, args ...string) error {
	f.args = append(f.args, append([]string(nil), args...))
	return nil
}

func fakeOutputKey(args ...string) string {
	key := ""
	for _, arg := range args {
		key += "\x00" + arg
	}
	return key
}

func TestMetadataAndArtists(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{}}
	runner.outputs[fakeOutputKey("-p", "mpv", "metadata", "--format", metadataFormat)] = "1500000\x1f180000000\x1f0.75\x1fPlaying\x1fTitle\x1fArtist A; Artist B\x1fAlbum\x1ffile:///tmp/art.png\x1ffalse\x1fNone\x1fmpv"
	runner.outputs[fakeOutputKey("-p", "mpv", "metadata", "xesam:artist")] = "Artist A\nArtist B"

	info, err := New(runner).Metadata(context.Background(), "mpv")
	if err != nil {
		t.Fatalf("Metadata returned error: %v", err)
	}
	if info.Position != 1 || info.Length != 180 || info.Volume != 75 {
		t.Fatalf("unexpected timing/volume: %#v", info)
	}
	if info.PositionSeconds != 1.5 {
		t.Fatalf("unexpected precise position: %v", info.PositionSeconds)
	}
	if info.LengthSeconds != 180 {
		t.Fatalf("unexpected precise length: %v", info.LengthSeconds)
	}
	if info.Name != "mpv" || len(info.Artists) != 2 {
		t.Fatalf("unexpected player identity/artists: %#v", info)
	}
	artists, err := New(runner).Artists(context.Background(), "mpv")
	if err != nil {
		t.Fatalf("Artists returned error: %v", err)
	}
	if len(artists) != 2 || artists[1] != "Artist B" {
		t.Fatalf("unexpected artists: %#v", artists)
	}
}

func TestParseMetadataKeepsTextWhenOptionalNumbersAreInvalid(t *testing.T) {
	info, err := ParseMetadata("not-a-position\x1f\x1fnot-a-volume\x1fPaused\x1fA ;; B\x1fArtist\x1fAlbum\x1f\x1ffalse\x1fNone\x1fplayer", "fallback")
	if err != nil {
		t.Fatalf("ParseMetadata returned error: %v", err)
	}
	if info.Title != "A ;; B" || info.Name != "player" || info.PositionSeconds != 0 || info.Volume != 0 {
		t.Fatalf("optional numeric failure discarded metadata: %#v", info)
	}
}

func TestSnapshotPositionAt(t *testing.T) {
	received := time.Now()
	snapshot := Snapshot{Info: Info{PositionSeconds: 10, Length: 30, Status: "Playing"}, ReceivedAt: received}
	position := snapshot.PositionAt(received.Add(1500 * time.Millisecond))
	if math.Abs(position-11.5) > 0.001 {
		t.Fatalf("PositionAt = %v, want 11.5", position)
	}
	paused := snapshot
	paused.Info.Status = "Paused"
	if got := paused.PositionAt(received.Add(10 * time.Second)); got != 10 {
		t.Fatalf("paused PositionAt = %v, want 10", got)
	}
	ended := snapshot
	ended.Info.PositionSeconds = 29.9
	if got := ended.PositionAt(received.Add(2 * time.Second)); got != 30 {
		t.Fatalf("PositionAt did not clamp to length: %v", got)
	}
	legacy := Snapshot{Info: Info{Position: 7, Length: 30, Status: "Playing"}}
	if got := legacy.PositionAt(time.Now()); got != 7 {
		t.Fatalf("legacy Position fallback = %v, want 7", got)
	}
}

func TestActionArgsAndNextLoop(t *testing.T) {
	args, ok := ActionArgs("volume-up")
	if !ok || len(args) != 2 || args[0] != "volume" || args[1] != "0.05+" {
		t.Fatalf("unexpected volume action: %v %v", args, ok)
	}
	if _, ok := ActionArgs("unknown"); ok {
		t.Fatal("unknown action was accepted")
	}
	if NextLoop("None") != "Track" || NextLoop("Track") != "Playlist" || NextLoop("Playlist") != "None" {
		t.Fatal("loop sequence is incorrect")
	}
}

func TestSplitArtistsDoesNotBreakSlashNames(t *testing.T) {
	artists := SplitArtistsFallback("AC/DC / Guest Artist")
	if len(artists) != 2 || artists[0] != "AC/DC" || artists[1] != "Guest Artist" {
		t.Fatalf("unexpected artist split: %#v", artists)
	}
}

func TestFollowReadsEventStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("playerctl is not supported on Windows")
	}
	script := "#!/bin/sh\nprintf '1000000\\03760000000\\0370.5\\037Playing\\037Title\\037Artist\\037Album\\037\\037false\\037None\\037fake\\036'\n"
	path := t.TempDir() + "/playerctl"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events := NewWithTimeout(ExecRunner{Command: path}, time.Second).Follow(ctx, "%any")
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("follow closed before receiving a snapshot")
			}
			if event.Snapshot != nil {
				if event.Snapshot.Info.Name != "fake" || event.Snapshot.Info.Title != "Title" {
					t.Fatalf("unexpected followed snapshot: %#v", event.Snapshot.Info)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("follow did not receive a snapshot")
		}
	}
}

func TestControl(t *testing.T) {
	runner := &fakeRunner{}
	if err := New(runner).Control(context.Background(), "mpv", "next"); err != nil {
		t.Fatalf("Control returned error: %v", err)
	}
	if len(runner.args) != 1 || len(runner.args[0]) != 3 || runner.args[0][0] != "-p" || runner.args[0][2] != "next" {
		t.Fatalf("unexpected playerctl args: %#v", runner.args)
	}
}

func TestClientAppliesPerCommandTimeout(t *testing.T) {
	started := time.Now()
	_, err := NewWithTimeout(blockingRunner{}, 20*time.Millisecond).Output(context.Background(), "status")
	if err == nil {
		t.Fatal("blocking command unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("command timeout was not applied: %v", elapsed)
	}
}

func TestSelectPrefersPlayingPlayer(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		fakeOutputKey("-l"):                      "paused playing",
		fakeOutputKey("-p", "paused", "status"):  "Paused",
		fakeOutputKey("-p", "playing", "status"): "Playing",
	}}
	selected, players, err := New(runner).Select(context.Background(), "")
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if selected != "playing" || len(players) != 2 {
		t.Fatalf("unexpected selection: %q %v", selected, players)
	}
}
