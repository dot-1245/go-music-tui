package player

import (
	"math"
	"sort"
	"time"
)

// CompetitionMethod isolates the new tournament from the earlier exploratory
// clocks and from production playback. All methods use receipt-time anchoring;
// a query's midpoint is not a guaranteed sample timestamp in MPRIS.
type CompetitionMethod string

const (
	SyncPeriodic CompetitionMethod = "periodic"
	SyncGentle   CompetitionMethod = "gentle"
	SyncMedian   CompetitionMethod = "median-3"
	SyncPLL      CompetitionMethod = "pll"
)

// PositionSample contains only information available to an MPRIS client.
// Generation is assigned locally when issuing a request, not by the player.
type PositionSample struct {
	Position, Rate        float64
	Track, Status         string
	StartedAt, FinishedAt time.Time
	Generation            uint64
}

type phaseSample struct {
	position float64
	at       time.Time
}

// CompetitionClock is single-owner, causal state shared by the tournament and
// replay. Constants are fixed before evaluation, not tuned per scenario.
type CompetitionClock struct {
	Method         CompetitionMethod
	sample         PositionSample
	ready          bool
	offset         float64
	settleUntil    time.Time
	rateCorrection float64
	history        []phaseSample
}

func (c *CompetitionClock) PositionAt(now time.Time) float64 {
	if !c.ready {
		return math.NaN()
	}
	p := c.sample.Position
	if c.sample.Status == "Playing" {
		elapsed := math.Max(0, now.Sub(c.sample.FinishedAt).Seconds())
		p += elapsed * (c.sample.Rate + c.rateCorrection)
		if now.Before(c.settleUntil) {
			p += c.offset * math.Min(1, c.settleUntil.Sub(now).Seconds()/.5)
		}
	}
	return math.Max(0, p)
}

func validPositionSample(s PositionSample) bool {
	return !math.IsNaN(s.Position) && !math.IsInf(s.Position, 0) && s.Position >= 0 &&
		!math.IsNaN(s.Rate) && !math.IsInf(s.Rate, 0) && s.Rate > 0 &&
		!s.StartedAt.IsZero() && !s.FinishedAt.Before(s.StartedAt) && s.Track != "" &&
		(s.Status == "Playing" || s.Status == "Paused" || s.Status == "Stopped")
}

// Transport applies a known MPRIS transport event. All methods reset equally.
// Later replies from an earlier generation cannot override the event.
func (c *CompetitionClock) Transport(s PositionSample) bool {
	if !validPositionSample(s) || (c.ready && (s.Generation < c.sample.Generation || s.FinishedAt.Before(c.sample.FinishedAt))) {
		return false
	}
	c.sample = s
	c.ready = true
	c.offset = 0
	c.rateCorrection = 0
	c.settleUntil = s.FinishedAt
	c.history = []phaseSample{{s.Position, s.FinishedAt}}
	return true
}

func (c *CompetitionClock) Observe(s PositionSample) bool {
	if !validPositionSample(s) {
		return false
	}
	if c.ready && (s.Generation < c.sample.Generation || s.StartedAt.Before(c.sample.FinishedAt) || !s.FinishedAt.After(c.sample.FinishedAt)) {
		return false
	}
	continuous := c.ready && s.Generation == c.sample.Generation && s.Track == c.sample.Track && s.Status == "Playing" && c.sample.Status == "Playing" && s.Rate == c.sample.Rate
	if !continuous {
		return c.Transport(s)
	}
	previous := c.PositionAt(s.FinishedAt)
	elapsed := s.FinishedAt.Sub(c.sample.FinishedAt).Seconds()
	target := s.Position
	switch c.Method {
	case SyncMedian:
		c.history = append(c.history, phaseSample{s.Position, s.FinishedAt})
		if len(c.history) > 3 {
			c.history = c.history[len(c.history)-3:]
		}
		values := make([]float64, 0, len(c.history))
		for _, h := range c.history {
			values = append(values, h.position+s.FinishedAt.Sub(h.at).Seconds()*s.Rate)
		}
		sort.Float64s(values)
		target = values[len(values)/2]
	case SyncPLL:
		if math.Abs(target-previous) <= .25 {
			error := target - previous
			c.rateCorrection = math.Max(-.01, math.Min(.01, c.rateCorrection+.02*error/elapsed))
			target = previous + .35*error
		} else {
			c.rateCorrection = 0
		}
	}
	c.offset = 0
	c.settleUntil = s.FinishedAt
	if c.Method == SyncGentle || c.Method == SyncMedian {
		if difference := previous - target; math.Abs(difference) <= .25 {
			c.offset = difference
			c.settleUntil = s.FinishedAt.Add(500 * time.Millisecond)
		}
	}
	s.Position = target
	c.sample = s
	c.ready = true
	return true
}
