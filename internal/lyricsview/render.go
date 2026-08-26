// Package lyricsview contains pure lyric presentation helpers shared by the
// TUI and manual debug commands.
package lyricsview

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
	"github.com/dot-1245/go-music-tui/internal/ttycolor"
)

// RenderKaraokeLine colors completed, active, and upcoming words using the
// semantic terminal theme. The text remains intact when colors are disabled.
func RenderKaraokeLine(line lyrics.Line, position float64, theme ttycolor.Theme) string {
	if len(line.Words) == 0 {
		return theme.Bold + theme.Primary + SafeText(line.Text) + theme.BoldOff + theme.Reset
	}

	currentWord := -1
	if !math.IsNaN(position) {
		firstAfter := sort.Search(len(line.Words), func(index int) bool {
			return line.Words[index].Time > position
		})
		currentWord = firstAfter - 1
	}

	var builder strings.Builder
	for i, word := range line.Words {
		color := theme.Gray
		if i < currentWord {
			color = theme.Accent
		} else if i == currentWord {
			color = theme.Primary
			builder.WriteString(theme.Bold)
		}
		builder.WriteString(color)
		builder.WriteString(SafeText(word.Text))
		if i == currentWord {
			builder.WriteString(theme.BoldOff)
		}
	}
	builder.WriteString(theme.Reset)
	return builder.String()
}

// SafeText removes terminal control characters from player/provider data.
// Titles and lyrics are external input and must not be allowed to inject
// cursor movement or alternate-screen escape sequences into the TUI.
func SafeText(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, value)
}
