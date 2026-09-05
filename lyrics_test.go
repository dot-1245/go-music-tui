package main

import (
	"math"
	"testing"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
)

func TestParseEnhancedLRC(t *testing.T) {
	lines := lyrics.ParseSyncedLyrics("[00:01.20]<00:01.20>Hello <00:01.50>world\n[00:03]Next")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0].Text != "Hello world" {
		t.Fatalf("line text = %q, want %q", lines[0].Text, "Hello world")
	}
	if len(lines[0].Words) != 2 {
		t.Fatalf("got %d words, want 2", len(lines[0].Words))
	}
	if math.Abs(lines[0].Time-1.2) > 0.0001 || math.Abs(lines[0].Words[0].Time-1.2) > 0.0001 {
		t.Fatalf("unexpected first timestamps: line=%v word=%v", lines[0].Time, lines[0].Words[0].Time)
	}
	if math.Abs(lines[0].Words[0].EndTime-1.5) > 0.0001 {
		t.Fatalf("first word end = %v, want 1.5", lines[0].Words[0].EndTime)
	}
	if math.Abs(lines[0].EndTime-3) > 0.0001 {
		t.Fatalf("first line end = %v, want 3", lines[0].EndTime)
	}
}

func TestParseStandardLRC(t *testing.T) {
	lines := lyrics.ParseSyncedLyrics("[00:01.2]first\n[00:02.34]second")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if math.Abs(lines[0].Time-1.2) > 0.0001 || math.Abs(lines[1].Time-2.34) > 0.0001 {
		t.Fatalf("unexpected timestamps: %#v", lines)
	}
	if len(lines[0].Words) != 0 || len(lines[1].Words) != 0 {
		t.Fatalf("ordinary LRC unexpectedly produced word timings: %#v", lines)
	}
}

func TestParseLRCRejectsInvalidSecondField(t *testing.T) {
	lines := lyrics.ParseSyncedLyrics("[00:60]invalid\n[01:02]valid")
	if len(lines) != 1 || lines[0].Text != "valid" {
		t.Fatalf("invalid timestamp was accepted: %#v", lines)
	}
}

func TestInstrumentalTitleDetection(t *testing.T) {
	for _, title := range []string{"Song (Instrumental)", "Song - off vocal", "Karaoke"} {
		if !lyrics.IsInstrumentalTitle(title) {
			t.Fatalf("instrumental title was not detected: %q", title)
		}
	}
	if lyrics.IsInstrumentalTitle("Instrumentalize") {
		t.Fatal("ordinary title was misclassified as instrumental")
	}
}

func TestParseLyricsfile(t *testing.T) {
	source := `
version: 1
lines:
  - start_ms: 1000
    end_ms: 2500
    text: "hello world"
    words:
      - start_ms: 1000
        end_ms: 1500
        text: "hello "
      - start_ms: 1500
        text: "world"
`
	lines, err := lyrics.ParseLyricsfile(source)
	if err != nil {
		t.Fatalf("parseLyricsfile returned error: %v", err)
	}
	if len(lines) != 1 || len(lines[0].Words) != 2 {
		t.Fatalf("unexpected Lyricsfile result: %#v", lines)
	}
	if math.Abs(lines[0].Time-1) > 0.0001 || math.Abs(lines[0].EndTime-2.5) > 0.0001 {
		t.Fatalf("unexpected line timing: %#v", lines[0])
	}
	if math.Abs(lines[0].Words[1].EndTime-2.5) > 0.0001 {
		t.Fatalf("last word end = %v, want 2.5", lines[0].Words[1].EndTime)
	}
}

func TestParseTTMLLyrics(t *testing.T) {
	source := `<tt xmlns:ttm="urn:ttm"><body><div>
<p begin="00:00:01.000" end="00:00:03.000">
  <span begin="1.0s" end="1.5s">こん</span><span begin="1.5s">にちは</span>
  <span ttm:role="x-translation">hello</span>
</p>
</div></body></tt>`
	lines, err := lyrics.ParseTTMLLyrics(source)
	if err != nil {
		t.Fatalf("parseTTMLLyrics returned error: %v", err)
	}
	if len(lines) != 1 || len(lines[0].Words) != 2 {
		t.Fatalf("unexpected TTML result: %#v", lines)
	}
	if lines[0].Text != "こんにちは" {
		t.Fatalf("line text = %q, want こんにちは", lines[0].Text)
	}
	if math.Abs(lines[0].Words[0].Time-1) > 0.0001 || math.Abs(lines[0].Words[0].EndTime-1.5) > 0.0001 {
		t.Fatalf("unexpected first word timing: %#v", lines[0].Words[0])
	}
}

func TestParseTTMLLyricsSeparatesAdjacentLatinSpans(t *testing.T) {
	source := `<tt><body><div><p begin="1s" end="3s"><span begin="1s">hello</span><span begin="2s">world</span></p></div></body></tt>`
	lines, err := lyrics.ParseTTMLLyrics(source)
	if err != nil {
		t.Fatalf("ParseTTMLLyrics returned error: %v", err)
	}
	if len(lines) != 1 || lines[0].Text != "hello world" {
		t.Fatalf("adjacent Latin spans were joined incorrectly: %#v", lines)
	}
}

func TestSyncLRCResult(t *testing.T) {
	payload := map[string]interface{}{
		"track":    "Test Track",
		"artist":   "Test Artist",
		"duration": float64(12),
		"type":     "karaoke",
		"lyrics":   "[00:00.00]<00:00.00>Test <00:00.50>Track\n[00:02.00]Next line",
	}
	result := lyrics.ParseSyncLRCResult(payload)
	if result == nil {
		t.Fatal("syncLRCResult returned nil")
	}
	if result.Source != "synclrc-enhanced" || result.Quality != 600 {
		t.Fatalf("unexpected result ranking: %#v", result)
	}
	if len(result.Lines) != 2 || len(result.Lines[0].Words) != 2 {
		t.Fatalf("unexpected result lines: %#v", result.Lines)
	}
}

func TestStripLeadingLyricMetadata(t *testing.T) {
	lines := lyrics.ParseSyncedLyrics("[00:00]<00:00>Track <00:00>(Artist)\n[00:03]Real lyric")
	lines = lyrics.StripLeadingLyricMetadata(lines, "Track", []string{"Artist"})
	if len(lines) != 1 || lines[0].Text != "Real lyric" {
		t.Fatalf("metadata was not stripped: %#v", lines)
	}
}

func TestBetterLyricResultPrefersWordSync(t *testing.T) {
	ordinary := &lyrics.Result{
		Title: "Track", Artist: "Artist", Album: "Album", Duration: 120,
		Lines: []lyrics.Line{{Time: 0, Text: "track"}}, Quality: 390,
	}
	karaoke := &lyrics.Result{
		Title: "Track", Artist: "Artist", Album: "Album", Duration: 120,
		Lines: []lyrics.Line{{Time: 0, Text: "track", Words: []lyrics.Word{{Time: 0, Text: "track"}}}}, Quality: 600,
	}
	if !lyrics.BetterResult(karaoke, ordinary, 120, "Track", []string{"Artist"}, "Album") {
		t.Fatal("word-synced result did not beat ordinary result")
	}
}

func TestBetterLyricResultAcceptsWordSyncWithoutProviderMetadata(t *testing.T) {
	ordinary := &lyrics.Result{
		Title: "Track", Artist: "Artist", Album: "Album", Duration: 120,
		Lines: []lyrics.Line{{Time: 0, Text: "track"}}, Quality: 390,
	}
	karaoke := &lyrics.Result{
		Lines: []lyrics.Line{{Time: 0, Text: "track", Words: []lyrics.Word{{Time: 0, Text: "track"}}}}, Quality: 600,
	}
	if !lyrics.BetterResult(karaoke, ordinary, 120, "Track", []string{"Artist"}, "Album") {
		t.Fatal("word-synced result without metadata did not beat ordinary result")
	}
}

func TestBetterLyricResultDoesNotDowngradeWordSync(t *testing.T) {
	ordinary := &lyrics.Result{
		Title: "Track", Artist: "Artist", Album: "Album", Duration: 120,
		Lines: []lyrics.Line{{Time: 0, Text: "track"}}, Quality: 390,
	}
	karaoke := &lyrics.Result{
		Lines: []lyrics.Line{{Time: 0, Text: "track", Words: []lyrics.Word{{Time: 0, Text: "track"}}}}, Quality: 600,
	}
	if lyrics.BetterResult(ordinary, karaoke, 120, "Track", []string{"Artist"}, "Album") {
		t.Fatal("ordinary result downgraded an existing word-synced result")
	}
}

func TestBetterLyricResultRejectsWeakerSyncLRCMetadata(t *testing.T) {
	ordinary := &lyrics.Result{
		Title: "2", Artist: "Lee Youngji", Album: "Gen", Duration: 160,
		Lines: []lyrics.Line{{Time: 1, Text: "correct"}}, Source: "lrclib-lyricsfile", Quality: 540,
	}
	karaoke := &lyrics.Result{
		Title: "2", Artist: "Lee Youngji", Duration: 0,
		Lines: []lyrics.Line{{Time: 1, Text: "wrong", Words: []lyrics.Word{{Time: 1, Text: "wrong"}}}}, Source: "synclrc-enhanced", Quality: 600,
	}
	if lyrics.BetterResult(karaoke, ordinary, 160, "2", []string{"星野源", "Lee Youngji"}, "Gen") {
		t.Fatal("metadata-weaker SyncLRC result replaced the better ordinary result")
	}
	if !lyrics.BetterResult(ordinary, karaoke, 160, "2", []string{"星野源", "Lee Youngji"}, "Gen") {
		t.Fatal("better ordinary result did not replace metadata-weaker SyncLRC result")
	}
}
