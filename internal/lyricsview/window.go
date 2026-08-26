package lyricsview

import (
	"math"
	"sort"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
)

// CurrentAndNext returns the active and next line indices for a playback
// position. An index of -1 means that no line is active/next.
func CurrentAndNext(lines []lyrics.Line, position float64) (current, next int) {
	if len(lines) == 0 || math.IsNaN(position) {
		return -1, -1
	}
	firstAfter := sort.Search(len(lines), func(index int) bool {
		return lines[index].Time > position
	})
	current = firstAfter - 1
	if current < 0 {
		current = -1
	}
	next = firstAfter
	if next >= len(lines) {
		next = -1
	}
	return current, next
}

// Window returns a centered lyric slice around active. It returns [start,end)
// and clamps the range to the available lines.
func Window(lineCount, active, maxLines int) (start, end int) {
	if lineCount <= 0 || maxLines <= 0 {
		return 0, 0
	}
	if active < 0 {
		active = 0
	}
	if active >= lineCount {
		active = lineCount - 1
	}
	if maxLines > lineCount {
		maxLines = lineCount
	}
	half := maxLines / 2
	start = active - half
	if start < 0 {
		start = 0
	}
	end = start + maxLines
	if end > lineCount {
		end = lineCount
		start = end - maxLines
		if start < 0 {
			start = 0
		}
	}
	return start, end
}
