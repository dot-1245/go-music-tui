package main

import (
	"context"
	"testing"
	"time"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
)

type blockingLyricProvider struct {
	started  chan string
	canceled chan string
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
