package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
	"github.com/dot-1245/go-music-tui/internal/lyricsview"
	"github.com/dot-1245/go-music-tui/internal/ttycolor"
)

const (
	sampleLRC        = "[00:01.00]<00:01.00>きょうは <00:01.50>いい天気\n[00:04.00]<00:04.00>歌を <00:04.50>うたおう"
	sampleTTML       = `<tt xmlns:ttm="http://www.w3.org/ns/ttml#metadata"><body><div><p begin="1s" end="3s"><span begin="1s" end="1.5s">きょうは </span><span begin="1.5s">いい天気</span></p><p begin="4s" end="6s"><span begin="4s" end="4.5s">歌を </span><span begin="4.5s">うたおう</span></p></div></body></tt>`
	sampleLyricsfile = `version: 1
lines:
  - start_ms: 1000
    end_ms: 3000
    text: "きょうは いい天気"
    words:
      - start_ms: 1000
        end_ms: 1500
        text: "きょうは "
      - start_ms: 1500
        text: "いい天気"
  - start_ms: 4000
    end_ms: 6000
    text: "歌を うたおう"
    words:
      - start_ms: 4000
        end_ms: 4500
        text: "歌を "
      - start_ms: 4500
        text: "うたおう"
`
)

func main() {
	fileName := flag.String("file", "", "lyrics file; defaults to a built-in sample for the selected format")
	format := flag.String("format", "lrc", "format: lrc, ttml, or lyricsfile")
	position := flag.Float64("position", 2.0, "playback position in seconds")
	maxLines := flag.Int("max-lines", 5, "number of lines to show")
	colorMode := flag.String("color", string(ttycolor.ModeAuto), "color mode: auto, always, or never")
	flag.Parse()

	mode, err := ttycolor.ParseMode(*colorMode)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *maxLines < 1 {
		fmt.Fprintln(os.Stderr, "-max-lines must be at least 1")
		os.Exit(2)
	}

	source := sampleForFormat(*format)
	if *fileName != "" {
		data, readErr := os.ReadFile(*fileName)
		if readErr != nil {
			fmt.Fprintln(os.Stderr, readErr)
			os.Exit(1)
		}
		source = string(data)
	}
	lines, err := parseLyrics(source, *format)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(lines) == 0 {
		fmt.Println("no lyric lines")
		return
	}

	theme := ttycolor.New(mode, os.Stdout)
	current, next := lyricsview.CurrentAndNext(lines, *position)
	active := current
	if active < 0 {
		active = next
	}
	start, end := lyricsview.Window(len(lines), active, *maxLines)
	fmt.Printf("format: %s lines=%d words=%d position=%.2fs current=%d next=%d\n", *format, len(lines), lyrics.CountWords(lines), *position, current, next)
	for i := start; i < end; i++ {
		prefix := "  "
		text := lines[i].Text
		if i == current {
			prefix = "> "
			text = lyricsview.RenderKaraokeLine(lines[i], *position, theme)
		}
		fmt.Printf("%s%02d %s\n", prefix, i, text)
	}
}

func sampleForFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "ttml", "xml":
		return sampleTTML
	case "lyricsfile", "yaml", "yml":
		return sampleLyricsfile
	default:
		return sampleLRC
	}
}

func parseLyrics(source, format string) ([]lyrics.Line, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "lrc", "lyrics":
		return lyrics.ParseSyncedLyrics(source), nil
	case "ttml", "xml":
		return lyrics.ParseTTMLLyrics(source)
	case "lyricsfile", "yaml", "yml":
		return lyrics.ParseLyricsfile(source)
	default:
		return nil, fmt.Errorf("unsupported lyrics format %q", format)
	}
}
