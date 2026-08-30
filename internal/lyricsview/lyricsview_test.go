package lyricsview

import (
	"strings"
	"testing"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
	"github.com/dot-1245/go-music-tui/internal/ttycolor"
)

func TestRenderKaraokeLine(t *testing.T) {
	theme := ttycolor.New(ttycolor.ModeAlways, nil)
	line := lyrics.Line{Words: []lyrics.Word{
		{Time: 1, Text: "one "},
		{Time: 2, Text: "two "},
		{Time: 3, Text: "three"},
	}}
	output := RenderKaraokeLine(line, 2.5, theme)
	if !strings.Contains(output, theme.Accent+"one ") || !strings.Contains(output, theme.Bold+theme.Primary+"two "+theme.BoldOff) || !strings.Contains(output, theme.Gray+"three") {
		t.Fatalf("unexpected karaoke colors: %q", output)
	}
	if !strings.HasSuffix(output, theme.Reset) {
		t.Fatalf("karaoke output has no reset: %q", output)
	}

	plainOutput := RenderKaraokeLine(lyrics.Line{Text: "ordinary LRC"}, 2.5, theme)
	if !strings.Contains(plainOutput, theme.Bold+theme.Primary+"ordinary LRC"+theme.BoldOff) {
		t.Fatalf("ordinary current line is not bold: %q", plainOutput)
	}
}

func TestSafeTextRemovesTerminalControls(t *testing.T) {
	if got := SafeText("title\x1b[2J\nnext\tpart"); got != "title[2J next part" {
		t.Fatalf("SafeText = %q", got)
	}
}

func TestWrapTextUsesTerminalCellWidth(t *testing.T) {
	if got := WrapText("abcdef", 3); len(got) != 2 || got[0] != "abc" || got[1] != "def" {
		t.Fatalf("ASCII WrapText = %#v", got)
	}
	if got := WrapText("あいう", 4); len(got) != 2 || got[0] != "あい" || got[1] != "う" {
		t.Fatalf("CJK WrapText = %#v", got)
	}
}

func TestRenderKaraokeLineWrappedKeepsRowsIndependent(t *testing.T) {
	theme := ttycolor.New(ttycolor.ModeAlways, nil)
	line := lyrics.Line{Words: []lyrics.Word{
		{Time: 1, Text: "one "},
		{Time: 2, Text: "two"},
	}}
	rows := RenderKaraokeLineWrapped(line, 2.5, theme, 4)
	if len(rows) != 2 {
		t.Fatalf("wrapped karaoke rows = %#v; want two rows", rows)
	}
	for index, row := range rows {
		if !strings.HasSuffix(row, theme.Reset) {
			t.Fatalf("row %d has no style reset: %q", index, row)
		}
	}
	if !strings.Contains(rows[0], "one ") || !strings.Contains(rows[1], "two") {
		t.Fatalf("wrapped karaoke text was split incorrectly: %#v", rows)
	}
}

func TestCurrentAndNextAndWindow(t *testing.T) {
	lines := []lyrics.Line{{Time: 1}, {Time: 3}, {Time: 5}}
	current, next := CurrentAndNext(lines, 3.5)
	if current != 1 || next != 2 {
		t.Fatalf("CurrentAndNext = %d, %d; want 1, 2", current, next)
	}
	start, end := Window(len(lines), current, 2)
	if start != 0 || end != 2 {
		t.Fatalf("Window = %d, %d; want 0, 2", start, end)
	}
}
