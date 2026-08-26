package main

import (
	"context"
	"image"
	"sync"
	"time"

	"github.com/dot-1245/go-music-tui/internal/artwork"
)

type artworkSnapshot struct {
	Source  string
	Image   image.Image
	Version uint64
	Loading bool
	Err     error
}

// artworkState owns the only in-flight artwork request. A new track cancels
// the previous request, while transient failures retry with a bounded
// backoff. The renderer only reads snapshots and never waits for I/O.
type artworkState struct {
	mu         sync.RWMutex
	fetcher    *artwork.Fetcher
	cache      *artwork.Cache
	source     string
	image      image.Image
	err        error
	loading    bool
	version    uint64
	generation uint64
	cancel     context.CancelFunc
	logf       func(string, ...interface{})
	wg         sync.WaitGroup
}

func newArtworkState(fetcher *artwork.Fetcher, logger func(string, ...interface{})) *artworkState {
	if fetcher == nil {
		fetcher = artwork.NewFetcher(nil)
	}
	return &artworkState{fetcher: fetcher, cache: artwork.NewCache(8), logf: logger}
}

func (state *artworkState) Request(parent context.Context, source string) {
	if parent == nil {
		parent = context.Background()
	}
	state.mu.Lock()
	if state.source == source {
		state.mu.Unlock()
		return
	}
	if state.cancel != nil {
		state.cancel()
	}
	state.source = source
	state.image = nil
	state.err = nil
	state.loading = false
	state.generation++
	generation := state.generation
	state.version++
	state.cancel = nil
	if source == "" {
		state.mu.Unlock()
		return
	}
	if imageValue, ok := state.cache.Get(source); ok {
		state.image = imageValue
		state.version++
		state.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	state.cancel = cancel
	state.loading = true
	state.wg.Add(1)
	state.mu.Unlock()

	go func() {
		defer state.wg.Done()
		state.load(ctx, source, generation)
	}()
}

func (state *artworkState) load(ctx context.Context, source string, generation uint64) {
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		imageValue, err := state.fetcher.Fetch(attemptCtx, source)
		cancel()
		if err == nil {
			state.cache.Put(source, imageValue)
			state.mu.Lock()
			if state.generation == generation && state.source == source {
				state.image = imageValue
				state.err = nil
				state.loading = false
				state.version++
			}
			state.mu.Unlock()
			return
		}
		if ctx.Err() != nil {
			return
		}
		state.mu.Lock()
		if state.generation == generation && state.source == source {
			state.err = err
			state.loading = true
			state.version++
		}
		state.mu.Unlock()
		if state.logf != nil {
			state.logf("artwork fetch failed (attempt=%d source=%q): %v", attempt, source, err)
		}
		if !waitContext(ctx, backoff) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (state *artworkState) Snapshot() artworkSnapshot {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return artworkSnapshot{
		Source:  state.source,
		Image:   state.image,
		Version: state.version,
		Loading: state.loading,
		Err:     state.err,
	}
}

func (state *artworkState) Close() {
	state.mu.Lock()
	if state.cancel != nil {
		state.cancel()
		state.cancel = nil
	}
	state.mu.Unlock()
	state.wg.Wait()
}
