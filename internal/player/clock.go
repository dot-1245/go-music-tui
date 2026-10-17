package player

import (
	"context"
	"math"
	"time"
)

// ClockStrategy selects an experimental clock for the sync-clock comparison.
type ClockStrategy string

const (
	ClockReceipt  ClockStrategy = "receipt"
	ClockMidpoint ClockStrategy = "midpoint"
	ClockSlew     ClockStrategy = "slew"
	ClockBounded  ClockStrategy = "bounded-slew"
	ClockInterval ClockStrategy = "interval"
)

// Observation bounds when the player's position was sampled. Midpoint timing
// is an estimate, not a measurement of the server's sampling instant.
type Observation struct {
	Snapshot   Snapshot
	StartedAt  time.Time
	FinishedAt time.Time
}

// ObservePosition reads playback state without changing playback. Keeping the
// complete metadata associates position with the track and playing/paused state.
func (c *Client) ObservePosition(ctx context.Context, name string) (Observation, error) {
	start := time.Now()
	info, err := c.Metadata(ctx, name)
	end := time.Now()
	return Observation{Snapshot: Snapshot{Info: info, ReceivedAt: end}, StartedAt: start, FinishedAt: end}, err
}

// PlaybackClock is single-owner state used by diagnostic experiments. It does
// not alter Follow or the production renderer until a strategy is validated.
type PlaybackClock struct {
	Strategy ClockStrategy
	snapshot Snapshot
	last     time.Time
	settleAt time.Time
	offset   float64
	ready    bool
}

func (c *PlaybackClock) PositionAt(now time.Time) float64 {
	if !c.ready {
		return math.NaN()
	}
	position := c.snapshot.PositionAt(now)
	if now.Before(c.settleAt) && isPlaying(c.snapshot.Info) {
		remaining := c.settleAt.Sub(now).Seconds() / 0.5
		if remaining > 1 {
			remaining = 1
		}
		position += c.offset * remaining
	}
	length := c.snapshot.Info.LengthSeconds
	if length <= 0 {
		length = float64(c.snapshot.Info.Length)
	}
	if length > 0 && position > length {
		position = length
	}
	return math.Max(0, position)
}

// Observe returns false for invalid, out-of-order or (for the bounded strategy)
// slow measurements. Small corrections settle in 500ms; seeks and state changes
// re-anchor immediately so smoothing cannot drag a pause or repeat backwards.
func (c *PlaybackClock) Observe(o Observation) bool {
	position := o.Snapshot.Info.PositionSeconds
	if math.IsNaN(position) || math.IsInf(position, 0) || position < 0 ||
		o.StartedAt.IsZero() || o.FinishedAt.Before(o.StartedAt) ||
		(c.ready && (!o.FinishedAt.After(c.last) || o.StartedAt.Before(c.last))) {
		return false
	}
	rtt := o.FinishedAt.Sub(o.StartedAt)
	if (c.Strategy == ClockBounded || c.Strategy == ClockInterval) && rtt > 250*time.Millisecond {
		return false
	}
	next := o.Snapshot
	next.ReceivedAt = o.FinishedAt
	if c.Strategy != ClockReceipt {
		next.ReceivedAt = o.StartedAt.Add(rtt / 2)
	}
	previous := c.PositionAt(o.FinishedAt)
	continuous := c.ready && sameTrack(c.snapshot.Info, next.Info) && isPlaying(c.snapshot.Info) && isPlaying(next.Info)
	if c.Strategy == ClockInterval && continuous {
		// Sampling occurred somewhere inside the query. Only correct when the
		// current estimate falls outside that uncertainty interval; do not chase
		// the midpoint as if it were an exact timestamp.
		lower := o.Snapshot.Info.PositionSeconds
		upper := lower + rtt.Seconds()
		position := math.Max(lower, math.Min(previous, upper))
		next.Info.PositionSeconds = position
		next.Info.Position = int(position)
		next.ReceivedAt = o.FinishedAt
	}
	if c.Strategy == ClockReceipt && continuous {
		next = ReconcileSnapshot(c.snapshot, next, o.FinishedAt)
	}
	c.offset = 0
	c.settleAt = o.FinishedAt
	if continuous && (c.Strategy == ClockSlew || c.Strategy == ClockBounded || c.Strategy == ClockInterval) {
		difference := previous - next.PositionAt(o.FinishedAt)
		if math.Abs(difference) <= 0.25 {
			c.offset = difference
			c.settleAt = o.FinishedAt.Add(500 * time.Millisecond)
		}
	}
	c.snapshot = next
	c.last = o.FinishedAt
	c.ready = true
	return true
}
