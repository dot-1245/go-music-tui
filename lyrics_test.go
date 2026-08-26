package main

import (
	"math"
	"testing"
)

func TestParseEnhancedLRC(t *testing.T) {
	lines := parseSyncedLyrics("[00:01.20]<00:01.20>Hello <00:01.50>world\n[00:03]Next")
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
	lines := parseSyncedLyrics("[00:01.2]first\n[00:02.34]second")
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
	lines, err := parseLyricsfile(source)
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
	lines, err := parseTTMLLyrics(source)
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

func TestSyncLRCResult(t *testing.T) {
	payload := map[string]interface{}{
		"track":    "Test Track",
		"artist":   "Test Artist",
		"duration": float64(12),
		"type":     "karaoke",
		"lyrics":   "[00:00.00]<00:00.00>Test <00:00.50>Track",
	}
	result := syncLRCResult(payload)
	if result == nil {
		t.Fatal("syncLRCResult returned nil")
	}
	if result.Source != "synclrc-enhanced" || result.Quality != 600 {
		t.Fatalf("unexpected result ranking: %#v", result)
	}
	if len(result.Lines) != 1 || len(result.Lines[0].Words) != 2 {
		t.Fatalf("unexpected result lines: %#v", result.Lines)
	}
}

func TestStripLeadingLyricMetadata(t *testing.T) {
	lines := parseSyncedLyrics("[00:00]<00:00>Track <00:00>(Artist)\n[00:03]Real lyric")
	lines = stripLeadingLyricMetadata(lines, "Track", []string{"Artist"})
	if len(lines) != 1 || lines[0].Text != "Real lyric" {
		t.Fatalf("metadata was not stripped: %#v", lines)
	}
}

func TestBetterLyricResultPrefersWordSync(t *testing.T) {
	ordinary := &lyricResult{
		Title: "Track", Artist: "Artist", Album: "Album", Duration: 120,
		Lines: []LyricLine{{Time: 0, Text: "track"}}, Quality: 390,
	}
	karaoke := &lyricResult{
		Title: "Track", Artist: "Artist", Album: "Album", Duration: 120,
		Lines: []LyricLine{{Time: 0, Text: "track", Words: []LyricWord{{Time: 0, Text: "track"}}}}, Quality: 600,
	}
	if !betterLyricResult(karaoke, ordinary, 120, "Track", []string{"Artist"}, "Album") {
		t.Fatal("word-synced result did not beat ordinary result")
	}
}
