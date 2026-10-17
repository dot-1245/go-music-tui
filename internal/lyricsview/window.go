package lyricsview

import (
	"math"
	"sort"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
)

// CurrentAndNext returns the active and next line indices for a playback
// position. An index of -1 means that no line is active/next. When a line has
// a valid EndTime, the gap after that time is treated as inactive until the
// next line starts.
func CurrentAndNext(lines []lyrics.Line, position float64) (current, next int) {
	if len(lines) == 0 || math.IsNaN(position) {
		return -1, -1
	}
	firstAfter := sort.Search(len(lines), func(index int) bool {
		return lines[index].Time > position
	})
	current = firstAfter - 1
	for current >= 0 && lineHasEnded(lines[current], position) {
		current--
	}
	next = firstAfter
	if next >= len(lines) {
		next = -1
	}
	return current, next
}

func lineHasEnded(line lyrics.Line, position float64) bool {
	// A zero or reversed EndTime is the zero value used by ordinary LRC and
	// means that the provider did not supply an explicit line end.
	if math.IsNaN(line.EndTime) || math.IsInf(line.EndTime, 0) || line.EndTime <= line.Time {
		return false
	}
	return position >= line.EndTime
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
