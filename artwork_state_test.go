package main

import (
	"errors"
	"image"
	"testing"
)

func TestArtworkStateResetClearsVisibleArtworkAndCache(t *testing.T) {
	state := newArtworkState(nil, nil)
	imageValue := image.NewRGBA(image.Rect(0, 0, 1, 1))
	state.mu.Lock()
	state.source = "cover"
	state.image = imageValue
	state.err = errors.New("stale artwork error")
	state.loading = true
	state.cache.Put("cover", imageValue)
	state.mu.Unlock()

	state.Reset()
	snapshot := state.Snapshot()
	if snapshot.Source != "" || snapshot.Image != nil || snapshot.Err != nil || snapshot.Loading {
		t.Fatalf("Reset snapshot = %#v; want empty artwork state", snapshot)
	}
	if _, ok := state.cache.Get("cover"); ok {
		t.Fatal("Reset left the artwork cache populated")
	}
}
