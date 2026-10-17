package player

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ObserveRawPosition uses playerctl's explicit position command (MPRIS Get),
// not the interpolated {{position}} format property. Metadata on both sides
// guards track/state changes, but not same-track seeks or player-side caching.
// Timestamps bracket only the position request.
func (c *Client) ObserveRawPosition(ctx context.Context, name string) (Observation, error) {
	before, err := c.Metadata(ctx, name)
	if err != nil {
		return Observation{}, err
	}
	start := time.Now()
	value, err := c.Query(ctx, name, "position")
	end := time.Now()
	if err != nil {
		return Observation{}, err
	}
	position, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(position) || math.IsInf(position, 0) || position < 0 {
		return Observation{}, fmt.Errorf("invalid MPRIS position")
	}
	after, err := c.Metadata(ctx, name)
	if err != nil {
		return Observation{}, err
	}
	if (Snapshot{Info: before}).TrackKey() != (Snapshot{Info: after}).TrackKey() || before.Status != after.Status {
		return Observation{}, fmt.Errorf("track or state changed during position query")
	}
	after.PositionSeconds = position
	if position >= float64(math.MaxInt) {
		after.Position = math.MaxInt
	} else {
		after.Position = int(position)
	}
	return Observation{Snapshot: Snapshot{Info: after, ReceivedAt: end}, StartedAt: start, FinishedAt: end}, nil
}
