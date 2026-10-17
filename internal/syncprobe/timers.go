package syncprobe

import (
	"time"

	"github.com/dot-1245/go-music-tui/internal/lyricsview"
	"github.com/dot-1245/go-music-tui/internal/player"
)

// CompareTimers measures wake-up lateness on this host, not terminal paint or
// audio latency. Both workers share the same deadlines and run concurrently.
func CompareTimers() []Metrics {
	start := time.Now().Add(100 * time.Millisecond)
	var boundaries []float64
	for i := 0; i < 60; i++ {
		boundaries = append(boundaries, .023+float64(i)*.037)
	}
	results := make(chan Metrics, 2)
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		var errors []float64
		next := 0
		for range ticker.C {
			elapsed := time.Since(start).Seconds()
			for next < len(boundaries) && boundaries[next] <= elapsed {
				errors = append(errors, elapsed-boundaries[next])
				next++
			}
			if next == len(boundaries) {
				break
			}
		}
		results <- summarize("host-wakeup", player.ClockStrategy("ticker-50ms"), errors, 0, 0)
	}()
	go func() {
		var errors []float64
		for _, boundary := range boundaries {
			delay, _ := lyricsview.NextBoundaryDelay([]float64{boundary}, time.Since(start).Seconds(), 1)
			timer := time.NewTimer(delay)
			<-timer.C
			errors = append(errors, time.Since(start).Seconds()-boundary)
		}
		results <- summarize("host-wakeup", player.ClockStrategy("boundary-timer"), errors, 0, 0)
	}()
	return []Metrics{<-results, <-results}
}
