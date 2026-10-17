package lyricsview

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
)

func TestBoundariesSortsDeduplicatesAndFiltersInvalidValues(t *testing.T) {
	lines := []lyrics.Line{
		{Time: 2, EndTime: 4, Words: []lyrics.Word{{Time: 3, EndTime: 3.5}, {Time: 2}}},
		{Time: 1, EndTime: 0.5, Words: []lyrics.Word{{Time: 1, EndTime: 1.5}}},
		{Time: math.NaN(), EndTime: math.Inf(1), Words: []lyrics.Word{{Time: -1}, {Time: math.Inf(-1)}}},
	}
	want := []float64{0, 1, 1.5, 2, 3, 3.5, 4, 4.25}
	if got := Boundaries(lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("Boundaries = %#v, want %#v", got, want)
	}
}

func TestNextBoundaryDelayExactBoundaryAndLineEndGrace(t *testing.T) {
	boundaries := Boundaries([]lyrics.Line{{Time: 1, EndTime: 2}})
	tests := []struct {
		name     string
		position float64
		want     time.Duration
	}{
		{name: "before line", position: 0.5, want: 500 * time.Millisecond},
		{name: "exact start advances to next event", position: 1, want: time.Second},
		{name: "exact end advances to grace event", position: 2, want: 250 * time.Millisecond},
		{name: "line end plus quarter", position: 2.25, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := NextBoundaryDelay(boundaries, test.position, 1)
			if test.position == 2.25 {
				if ok {
					t.Fatalf("delay at final exact boundary = %v, want no future boundary", got)
				}
				return
			}
			if !ok || got != test.want {
				t.Fatalf("NextBoundaryDelay = %v, %v; want %v, true", got, ok, test.want)
			}
		})
	}
}

func TestNextBoundaryDelayPauseAndInvalidInputs(t *testing.T) {
	boundaries := []float64{1, 2}
	for _, rate := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if delay, ok := NextBoundaryDelay(boundaries, 0, rate); ok || delay != 0 {
			t.Errorf("rate %v returned (%v, %v), want (0, false)", rate, delay, ok)
		}
	}
	for _, position := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if delay, ok := NextBoundaryDelay(boundaries, position, 1); ok || delay != 0 {
			t.Errorf("position %v returned (%v, %v), want (0, false)", position, delay, ok)
		}
	}
	if delay, ok := NextBoundaryDelay(boundaries, 2, 1); ok || delay != 0 {
		t.Errorf("no-future-boundary result = (%v, %v), want (0, false)", delay, ok)
	}
}

func TestNextBoundaryDelayNeverReturnsNegativeDelay(t *testing.T) {
	boundaries := []float64{0, 0.1, 0.2}
	for _, position := range []float64{-1, 0, 0.1, 0.2} {
		delay, ok := NextBoundaryDelay(boundaries, position, 3.5)
		if ok && delay < 0 {
			t.Errorf("position %v produced negative delay %v", position, delay)
		}
	}
}
