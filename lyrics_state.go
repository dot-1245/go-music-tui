package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
)

type LyricWord = lyrics.Word
type LyricLine = lyrics.Line
type lyricResult = lyrics.Result

type lyricProvider interface {
	FetchLRCLIB(context.Context, string, []string, string, int) *lyrics.Result
	FetchSyncLRC(context.Context, string, string, []string, string, int) *lyrics.Result
	FetchAMLL(context.Context, string, string, []string, string, int) *lyrics.Result
}

type lyricsState struct {
	mu       sync.RWMutex
	lines    []LyricLine
	loading  bool
	fetching bool
	request  uint64
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	cache    map[string][]LyricLine
	order    []string
}

// lyricFetchTimeout bounds one provider round. AMLL may need several
// sequential search/get requests before it can return word-synchronized
// lyrics, so the runtime deadline must cover that plan rather than only one
// HTTP request.
const lyricFetchTimeout = 5 * time.Second

func newLyricsState() *lyricsState {
	return &lyricsState{cache: make(map[string][]LyricLine)}
}

// Start replaces the active lookup and cancels every provider request for the
// previous track. Results are accepted only while their request generation is
// current, so a late response cannot overwrite a newer song.
func (state *lyricsState) Start(parent context.Context, client lyricProvider, title string, artists []string, rawArtist, album string, durationSec int) {
	state.start(parent, client, title, artists, rawArtist, album, durationSec, true, false)
}

// Refresh retries the current lookup while bypassing the lyric cache. Existing
// lines remain visible if the providers do not return a usable result. This is
// used to discover enhanced lyrics that were unavailable during the first
// lookup.
func (state *lyricsState) Refresh(parent context.Context, client lyricProvider, title string, artists []string, rawArtist, album string, durationSec int) {
	state.start(parent, client, title, artists, rawArtist, album, durationSec, false, true)
}

func (state *lyricsState) start(parent context.Context, client lyricProvider, title string, artists []string, rawArtist, album string, durationSec int, useCache, preserveOnFailure bool) {
	if parent == nil {
		parent = context.Background()
	}
	cacheKey := lyricCacheKey(title, artists, rawArtist, album, durationSec)
	state.mu.Lock()
	if state.cancel != nil {
		state.cancel()
	}
	state.request++
	requestID := state.request
	if !preserveOnFailure {
		state.lines = []LyricLine{{Time: 0, Text: "Loading lyrics..."}}
	}
	state.loading = true
	state.fetching = true
	state.cancel = nil
	if useCache {
		if cached, ok := state.cache[cacheKey]; ok {
			state.lines = cloneLyricLines(cached)
			state.loading = false
			state.fetching = false
			state.promoteCacheLocked(cacheKey)
			state.mu.Unlock()
			return
		}
	}
	if client == nil || title == "" {
		if !preserveOnFailure {
			state.lines = nil
		}
		state.loading = false
		state.fetching = false
		state.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	state.cancel = cancel
	state.wg.Add(1)
	state.mu.Unlock()

	go func() {
		defer state.wg.Done()
		defer state.finish(requestID)
		state.fetch(ctx, client, title, artists, rawArtist, album, durationSec, requestID, cacheKey, preserveOnFailure)
	}()
}

func (state *lyricsState) finish(requestID uint64) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.request != requestID {
		return
	}
	state.fetching = false
	state.loading = false
	state.cancel = nil
}

// NeedsRefresh reports whether a completed lookup may be improved or retried
// later. An empty result is retryable too: a transient provider failure must
// not permanently suppress the periodic recheck.
func (state *lyricsState) NeedsRefresh() bool {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return !state.fetching && !lyrics.HasWordSyncedLyrics(state.lines)
}

func (state *lyricsState) fetch(ctx context.Context, client lyricProvider, title string, artists []string, rawArtist, album string, durationSec int, requestID uint64, cacheKey string, preserveOnFailure bool) {
	debugf("=== fetch start: title=%q artists=%v rawArtist=%q album=%q duration=%ds (reqID=%d)", title, artists, rawArtist, album, durationSec, requestID)
	if lyrics.IsInstrumentalTitle(title) {
		debugf("=> skipped: title matched instrumental pattern")
		state.apply(nil, requestID, cacheKey, preserveOnFailure)
		return
	}

	targetArtists := make([]string, 0, len(artists)+1)
	targetArtists = appendUniqueArtist(targetArtists, rawArtist)
	for _, artist := range artists {
		targetArtists = appendUniqueArtist(targetArtists, artist)
	}

	requestCtx, cancel := context.WithTimeout(ctx, lyricFetchTimeout)
	defer cancel()
	results := make(chan *lyricResult, 3)
	var providerWaitGroup sync.WaitGroup
	providerWaitGroup.Add(3)
	defer providerWaitGroup.Wait()
	go func() {
		defer providerWaitGroup.Done()
		results <- client.FetchLRCLIB(requestCtx, title, artists, album, durationSec)
	}()
	go func() {
		defer providerWaitGroup.Done()
		results <- client.FetchSyncLRC(requestCtx, title, rawArtist, artists, album, durationSec)
	}()
	go func() {
		defer providerWaitGroup.Done()
		results <- client.FetchAMLL(requestCtx, title, rawArtist, artists, album, durationSec)
	}()

	var best *lyricResult
	completed := 0
	for completed < 3 {
		select {
		case <-ctx.Done():
			return
		case <-requestCtx.Done():
			if best == nil {
				state.apply(nil, requestID, cacheKey, preserveOnFailure)
			}
			return
		case result := <-results:
			completed++
			if result != nil {
				result.Lines = lyrics.StripLeadingLyricMetadata(result.Lines, title, targetArtists)
				if len(result.Lines) == 0 {
					result = nil
				}
				if preserveOnFailure && result != nil && !lyrics.HasWordSyncedLyrics(result.Lines) {
					// A periodic refresh is specifically looking for an
					// enhanced result. Do not replace the visible lyrics with
					// another ordinary result while looking for one.
					result = nil
				}
			}
			if result != nil && lyrics.BetterResult(result, best, durationSec, lyrics.CleanTrackTitle(title), targetArtists, album) {
				best = result
				debugf("=> applied provider result: source=%s title=%q artist=%q (reqID=%d)", result.Source, result.Title, result.Artist, requestID)
				state.apply(result, requestID, cacheKey, preserveOnFailure)
			}
		}
	}
	if best == nil {
		state.apply(nil, requestID, cacheKey, preserveOnFailure)
	}
}

func (state *lyricsState) apply(result *lyricResult, requestID uint64, cacheKey string, preserveOnFailure bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.request != requestID {
		return
	}
	state.loading = false
	if result == nil {
		if !preserveOnFailure {
			state.lines = nil
		}
		return
	}
	state.lines = cloneLyricLines(result.Lines)
	if cacheKey != "" {
		state.cache[cacheKey] = cloneLyricLines(result.Lines)
		state.promoteCacheLocked(cacheKey)
	}
}

func (state *lyricsState) Snapshot() (lines []LyricLine, loading bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return cloneLyricLines(state.lines), state.loading
}

func (state *lyricsState) Stop() {
	state.mu.Lock()
	if state.cancel != nil {
		state.cancel()
		state.cancel = nil
	}
	state.request++
	state.lines = nil
	state.loading = false
	state.fetching = false
	state.mu.Unlock()
	state.wg.Wait()
}

// Reset stops the active lookup, clears visible lyrics, and drops cached
// results so the next Start always performs a fresh provider lookup.
func (state *lyricsState) Reset() {
	state.Stop()
	state.mu.Lock()
	state.cache = make(map[string][]LyricLine)
	state.order = nil
	state.mu.Unlock()
}

func lyricCacheKey(title string, artists []string, rawArtist, album string, durationSec int) string {
	values := []string{title, rawArtist, album, strconv.Itoa(durationSec), strings.Join(artists, "\x00")}
	return strings.Join(values, "\x00")
}

func (state *lyricsState) promoteCacheLocked(key string) {
	for index, existing := range state.order {
		if existing == key {
			state.order = append(state.order[:index], state.order[index+1:]...)
			break
		}
	}
	state.order = append(state.order, key)
	for len(state.order) > 16 {
		oldest := state.order[0]
		state.order = state.order[1:]
		delete(state.cache, oldest)
	}
}

func cloneLyricLines(lines []LyricLine) []LyricLine {
	if len(lines) == 0 {
		return nil
	}
	clone := make([]LyricLine, len(lines))
	for i, line := range lines {
		clone[i] = line
		clone[i].Words = append([]LyricWord(nil), line.Words...)
	}
	return clone
}

func newRuntimeLyricClient() *lyrics.Client {
	return lyrics.NewClientWithOptions(&http.Client{Timeout: 15 * time.Second}, debugf, lyrics.ClientOptions{
		MaxResponseBytes: 4 << 20,
		CaptureBody:      false,
	})
}

func appendUniqueArtist(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	if value != "" {
		return append(values, value)
	}
	return values
}
