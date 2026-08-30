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
	segments := karaokeSegments(line, position, theme)
	var builder strings.Builder
	for _, segment := range segments {
		builder.WriteString(segment.prefix)
		builder.WriteString(segment.text)
		builder.WriteString(segment.suffix)
	}
	builder.WriteString(theme.Reset)
	return builder.String()
}

// RenderKaraokeLineWrapped renders a karaoke line into independently
// positioned terminal rows. Explicitly wrapping here prevents the terminal
// from continuing at column one, where a wrapped lyric could overwrite album
// art and leave stale text behind on the next frame.
func RenderKaraokeLineWrapped(line lyrics.Line, position float64, theme ttycolor.Theme, maxWidth int) []string {
	return wrapSegments(karaokeSegments(line, position, theme), maxWidth, theme.Reset)
}

// WrapText splits sanitized text into rows no wider than maxWidth terminal
// cells. It uses the same East Asian and emoji width approximation as the
// karaoke renderer.
func WrapText(value string, maxWidth int) []string {
	return wrapSegments([]styledSegment{{text: SafeText(value)}}, maxWidth, "")
}

type styledSegment struct {
	prefix, text, suffix string
}

func karaokeSegments(line lyrics.Line, position float64, theme ttycolor.Theme) []styledSegment {
	if len(line.Words) == 0 {
		return []styledSegment{{prefix: theme.Bold + theme.Primary, text: SafeText(line.Text), suffix: theme.BoldOff}}
	}

	currentWord := -1
	if !math.IsNaN(position) {
		firstAfter := sort.Search(len(line.Words), func(index int) bool {
			return line.Words[index].Time > position
		})
		currentWord = firstAfter - 1
	}

	segments := make([]styledSegment, 0, len(line.Words))
	for i, word := range line.Words {
		color := theme.Gray
		prefix := color
		suffix := ""
		if i < currentWord {
			color = theme.Accent
			prefix = color
		} else if i == currentWord {
			color = theme.Primary
			prefix = theme.Bold + color
			suffix = theme.BoldOff
		}
		segments = append(segments, styledSegment{prefix: prefix, text: SafeText(word.Text), suffix: suffix})
	}
	return segments
}

func wrapSegments(segments []styledSegment, maxWidth int, reset string) []string {
	if maxWidth < 1 {
		maxWidth = 1
	}
	rows := []string{""}
	width := 0
	for _, segment := range segments {
		remaining := []rune(segment.text)
		if len(remaining) == 0 {
			continue
		}
		for len(remaining) > 0 {
			if width >= maxWidth {
				rows = append(rows, "")
				width = 0
			}
			available := maxWidth - width
			count := 0
			used := 0
			for count < len(remaining) {
				cellWidth := runeCellWidth(remaining[count])
				if cellWidth < 1 {
					count++
					continue
				}
				if cellWidth > available-used {
					if used == 0 && width == 0 {
						// A wide rune cannot be split. Let it occupy its natural
						// width on an otherwise empty row rather than undercounting
						// it and allowing the terminal to wrap it implicitly.
						used = cellWidth
						count++
					}
					break
				}
				used += cellWidth
				count++
			}
			if count == 0 {
				rows = append(rows, "")
				width = 0
				continue
			}
			chunk := string(remaining[:count])
			rows[len(rows)-1] += segment.prefix + chunk + segment.suffix + reset
			width += used
			remaining = remaining[count:]
		}
	}
	return rows
}

func runeCellWidth(character rune) int {
	if character == 0 || unicode.IsControl(character) || unicode.Is(unicode.Mn, character) || unicode.Is(unicode.Me, character) {
		return 0
	}
	if (character >= 0x1100 && character <= 0x115f) ||
		(character >= 0x2329 && character <= 0x232a) ||
		(character >= 0x2e80 && character <= 0xa4cf) ||
		(character >= 0xac00 && character <= 0xd7a3) ||
		(character >= 0xf900 && character <= 0xfaff) ||
		(character >= 0xfe10 && character <= 0xfe6f) ||
		(character >= 0xff00 && character <= 0xff60) ||
		(character >= 0x1f300 && character <= 0x1faff) {
		return 2
	}
	return 1
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
