package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
	"github.com/dot-1245/go-music-tui/internal/player"
	"github.com/dot-1245/go-music-tui/internal/ttycolor"
)

func TestBuildFrameInterpolatesPositionAndSanitizesText(t *testing.T) {
	received := time.Now()
	snapshot := player.Snapshot{
		Info: player.Info{
			Name:            "test\x1b[2J",
			Title:           "title",
			Artist:          "artist",
			Status:          "Playing",
			Length:          60,
			PositionSeconds: 1,
		},
		ReceivedAt: received,
	}
	lines := []lyrics.Line{{Time: 0, Text: "hello\x1b[2J", Words: []lyrics.Word{{Time: 0, Text: "hello\x1b[2J"}}}}
	var output bytes.Buffer
	err := buildFrame(&output, snapshot, true, received.Add(1500*time.Millisecond), 80, 24, nil, ttycolor.New(ttycolor.ModeNever, nil), lines, artworkSnapshot{}, false, frameOptions{NoArt: true})
	if err != nil {
		t.Fatalf("buildFrame returned error: %v", err)
	}
	text := output.String()
	if strings.Contains(text, "\x1b[2J") {
		t.Fatalf("frame contains an injected escape sequence: %q", text)
	}
	if !strings.Contains(text, "00:02 / 01:00") {
		t.Fatalf("frame does not use interpolated position: %q", text)
	}
}

func TestControlArgs(t *testing.T) {
	tests := []struct {
		key  byte
		info player.Info
		want []string
	}{
		{key: ' ', want: []string{"play-pause"}},
		{key: 'w', want: []string{"volume", "0.05+"}},
		{key: 'a', want: []string{"position", "5-"}},
		{key: 'z', want: []string{"shuffle", "Toggle"}},
		{key: 'x', info: player.Info{Loop: "Track"}, want: []string{"loop", "Playlist"}},
	}
	for _, test := range tests {
		got, ok := controlArgs(test.key, test.info)
		if !ok || !reflect.DeepEqual(got, test.want) {
			t.Errorf("controlArgs(%q, %#v) = %v, %t; want %v, true", test.key, test.info, got, ok, test.want)
		}
	}
	if _, ok := controlArgs('?', player.Info{}); ok {
		t.Fatal("unknown key was accepted")
	}
}

func TestBuildFramePreservesInternalVolumeStyles(t *testing.T) {
	received := time.Now()
	snapshot := player.Snapshot{
		Info: player.Info{
			Title:           "title",
			Status:          "Paused",
			Length:          60,
			Volume:          50,
			PositionSeconds: 1,
		},
		ReceivedAt: received,
	}
	var output bytes.Buffer
	theme := ttycolor.New(ttycolor.ModeAlways, nil)
	if err := buildFrame(&output, snapshot, true, received, 80, 24, nil, theme, nil, artworkSnapshot{}, false, frameOptions{NoArt: true}); err != nil {
		t.Fatalf("buildFrame returned error: %v", err)
	}
	text := output.String()
	if !strings.Contains(text, theme.Accent+"======") || !strings.Contains(text, theme.Gray+"------") {
		t.Fatalf("volume bar styles were sanitized: %q", text)
	}
}

func TestBuildFrameWipesWrappedTextRows(t *testing.T) {
	var output bytes.Buffer
	if err := buildFrame(&output, player.Snapshot{}, false, time.Now(), 80, 24, nil, ttycolor.New(ttycolor.ModeNever, nil), nil, artworkSnapshot{}, false, frameOptions{NoArt: true}); err != nil {
		t.Fatalf("buildFrame returned error: %v", err)
	}
	text := output.String()
	if !strings.Contains(text, "\033[1;4H\033[K") || !strings.Contains(text, "\033[24;4H\033[K") {
		t.Fatalf("text area was not wiped from top to bottom: %q", text)
	}
}

func TestBuildFrameWrapsLyricsInsideTextPanel(t *testing.T) {
	received := time.Now()
	snapshot := player.Snapshot{
		Info:       player.Info{Status: "Playing", Title: "title", Length: 60},
		ReceivedAt: received,
	}
	lines := []lyrics.Line{{Time: 0, Text: strings.Repeat("x", 100)}}
	var output bytes.Buffer
	if err := buildFrame(&output, snapshot, true, received, 80, 24, nil, ttycolor.New(ttycolor.ModeNever, nil), lines, artworkSnapshot{}, false, frameOptions{NoArt: true}); err != nil {
		t.Fatalf("buildFrame returned error: %v", err)
	}
	text := output.String()
	if !strings.Contains(text, "\033[18;4H") {
		t.Fatalf("wrapped lyric did not get an explicit second row: %q", text)
	}
	if strings.Contains(text, "\033[18;1H") {
		t.Fatalf("wrapped lyric fell back to terminal column one: %q", text)
	}
}
