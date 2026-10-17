package main

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
	"github.com/dot-1245/go-music-tui/internal/player"
	"github.com/dot-1245/go-music-tui/internal/ttycolor"
)

func benchmarkWordSyncedLyrics() []LyricLine {
	lines := make([]LyricLine, 100)
	for lineIndex := range lines {
		lineStart := float64(lineIndex) * 4
		line := LyricLine{Time: lineStart, EndTime: lineStart + 3.8}
		for wordIndex := 0; wordIndex < 10; wordIndex++ {
			wordStart := lineStart + float64(wordIndex)*0.36
			word := fmt.Sprintf("word%02d ", wordIndex+1)
			line.Words = append(line.Words, lyrics.Word{
				Time: wordStart, EndTime: wordStart + 0.34, Text: word,
			})
			line.Text += word
		}
		lines[lineIndex] = line
	}
	return lines
}

func benchmarkFastRapLyrics() []LyricLine {
	line := LyricLine{Time: 0, EndTime: 3.2}
	for wordIndex := 0; wordIndex < 100; wordIndex++ {
		wordStart := float64(wordIndex) * 0.032
		word := fmt.Sprintf("rap%02d ", wordIndex+1)
		line.Words = append(line.Words, lyrics.Word{
			Time: wordStart, EndTime: wordStart + 0.03, Text: word,
		})
		line.Text += word
	}
	return []LyricLine{line}
}

func benchmarkBuildAndDiffFrame(b *testing.B, lyricLines []LyricLine, position float64) {
	b.Helper()
	now := time.Unix(1_700_000_000, 0)
	snapshot := player.Snapshot{
		Info: player.Info{
			Name: "benchmark-player", Title: "Benchmark Track", Artist: "Benchmark Artist",
			Album: "Benchmark Album", Status: "Playing", LengthSeconds: 300,
			PositionSeconds: position,
		},
		ReceivedAt: now,
	}
	theme := ttycolor.Theme{}
	options := frameOptions{NoArt: true}
	var frame bytes.Buffer
	var previousRows map[string]string

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.Reset()
		if err := buildFrame(&frame, snapshot, true, now, 120, 40, nil, theme, lyricLines, artworkSnapshot{}, false, options); err != nil {
			b.Fatal(err)
		}
		_, previousRows = changedFrameRows(frame.Bytes(), previousRows)
	}
}

func BenchmarkWordSyncedFrame(b *testing.B) {
	benchmarkBuildAndDiffFrame(b, benchmarkWordSyncedLyrics(), 202.137)
}

func BenchmarkFastRapFrame(b *testing.B) {
	benchmarkBuildAndDiffFrame(b, benchmarkFastRapLyrics(), 1.637)
}
