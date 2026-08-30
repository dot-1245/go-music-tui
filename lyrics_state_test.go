package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
)

type blockingLyricProvider struct {
	started  chan string
	canceled chan string
}

type upgradingLyricProvider struct {
	mu    sync.Mutex
	calls int
}

func (provider *upgradingLyricProvider) next() *lyrics.Result {
	provider.mu.Lock()
	provider.calls++
	enhanced := provider.calls > 3
	provider.mu.Unlock()
	result := &lyrics.Result{
		Title: "Track", Artist: "Artist", Album: "Album", Duration: 120,
		Lines: []lyrics.Line{{Time: 0, Text: "hello"}}, Quality: 390,
	}
	if enhanced {
		result.Quality = 600
		result.Lines[0].Words = []lyrics.Word{{Time: 0, Text: "hello"}}
	}
	return result
}

func (provider *upgradingLyricProvider) FetchLRCLIB(context.Context, string, []string, string, int) *lyrics.Result {
	return provider.next()
}

func (provider *upgradingLyricProvider) FetchSyncLRC(context.Context, string, string, []string, string, int) *lyrics.Result {
	return provider.next()
}

func (provider *upgradingLyricProvider) FetchAMLL(context.Context, string, string, []string, string, int) *lyrics.Result {
	return provider.next()
}

func (provider *blockingLyricProvider) wait(ctx context.Context, title string) *lyrics.Result {
	provider.started <- title
	<-ctx.Done()
	provider.canceled <- title
	return nil
}

func (provider *blockingLyricProvider) FetchLRCLIB(ctx context.Context, title string, _ []string, _ string, _ int) *lyrics.Result {
	return provider.wait(ctx, title)
}

func (provider *blockingLyricProvider) FetchSyncLRC(ctx context.Context, title, _ string, _ []string, _ string, _ int) *lyrics.Result {
	return provider.wait(ctx, title)
}

func (provider *blockingLyricProvider) FetchAMLL(ctx context.Context, title, _ string, _ []string, _ string, _ int) *lyrics.Result {
	return provider.wait(ctx, title)
}

func TestLyricsStateCancelsPreviousTrack(t *testing.T) {
	provider := &blockingLyricProvider{started: make(chan string, 8), canceled: make(chan string, 8)}
	state := newLyricsState()
	state.Start(context.Background(), provider, "first", []string{"artist"}, "artist", "album", 100)
	select {
	case title := <-provider.started:
		if title != "first" {
			t.Fatalf("unexpected first request title: %q", title)
		}
	case <-time.After(time.Second):
		t.Fatal("first lyric request did not start")
	}

	state.Start(context.Background(), provider, "second", []string{"artist"}, "artist", "album", 100)
	select {
	case title := <-provider.canceled:
		if title != "first" {
			t.Fatalf("canceled request title = %q, want first", title)
		}
	case <-time.After(time.Second):
		t.Fatal("previous lyric request was not canceled")
	}
	state.Stop()
}

func TestLyricsStateNeedsRefreshForNonWordSyncedLyrics(t *testing.T) {
	state := newLyricsState()
	if state.NeedsRefresh() {
		t.Fatal("empty lyrics were marked for recheck")
	}
	state.lines = []LyricLine{{Time: 0, Text: "line"}}
	if !state.NeedsRefresh() {
		t.Fatal("line-synchronized lyrics were not marked for recheck")
	}

	state.lines = []LyricLine{{Time: 0, Text: "line", Words: []LyricWord{{Time: 0, Text: "line"}}}}
	if state.NeedsRefresh() {
		t.Fatal("word-synchronized lyrics were marked for recheck")
	}

	state.fetching = true
	state.lines = []LyricLine{{Time: 0, Text: "line"}}
	if state.NeedsRefresh() {
		t.Fatal("active lyric fetch was marked for another recheck")
	}
}

func TestLyricsStateRefreshBypassesCacheForBetterLyrics(t *testing.T) {
	provider := &upgradingLyricProvider{}
	state := newLyricsState()
	defer state.Stop()
	state.Start(context.Background(), provider, "Track", []string{"Artist"}, "Artist", "Album", 120)

	deadline := time.Now().Add(time.Second)
	for !state.NeedsRefresh() {
		if time.Now().After(deadline) {
			t.Fatal("initial non-word-synced lyrics did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	state.Start(context.Background(), provider, "Track", []string{"Artist"}, "Artist", "Album", 120)
	lines, _ := state.Snapshot()
	if lyrics.HasWordSyncedLyrics(lines) {
		t.Fatal("cached lyrics unexpectedly became word-synced")
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 3 {
		t.Fatalf("cached Start made %d provider calls; want 3", calls)
	}

	state.Refresh(context.Background(), provider, "Track", []string{"Artist"}, "Artist", "Album", 120)
	deadline = time.Now().Add(time.Second)
	for {
		lines, _ := state.Snapshot()
		if lyrics.HasWordSyncedLyrics(lines) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh did not replace cached ordinary lyrics with word-synced lyrics")
		}
		time.Sleep(time.Millisecond)
	}
}
