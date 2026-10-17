package lyricsview

import (
	"math"
	"sort"
	"time"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
)

// Boundaries builds the lyric events that can change presentation. Call when
// lyrics change, not every frame. Word ends are included for future renderers.
func Boundaries(lines []lyrics.Line) []float64 {
	var events []float64
	add := func(value float64) {
		if !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 {
			events = append(events, value)
		}
	}
	for _, line := range lines {
		add(line.Time)
		if line.EndTime > line.Time {
			add(line.EndTime)
			add(line.EndTime + .25)
		}
		for _, word := range line.Words {
			add(word.Time)
			if word.EndTime > word.Time {
				add(word.EndTime)
			}
		}
	}
	sort.Float64s(events)
	unique := events[:0]
	for _, event := range events {
		if len(unique) == 0 || event != unique[len(unique)-1] {
			unique = append(unique, event)
		}
	}
	return unique
}

// NextBoundaryDelay avoids fixed-frame quantization. A clock correction, pause,
// seek or rate change must invalidate any previously armed timer.
func NextBoundaryDelay(boundaries []float64, position, rate float64) (time.Duration, bool) {
	if math.IsNaN(position) || math.IsInf(position, 0) || math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return 0, false
	}
	index := sort.Search(len(boundaries), func(i int) bool { return boundaries[i] > position })
	if index == len(boundaries) {
		return 0, false
	}
	seconds := (boundaries[index] - position) / rate
	if seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return time.Duration(math.MaxInt64), true
	}
	delay := time.Duration(math.Ceil(seconds * float64(time.Second)))
	return delay, true
}
