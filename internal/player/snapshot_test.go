package player

import (
	"math"
	"testing"
	"time"
)

func TestReconcileSnapshotSuppressesSmallBackwardJump(t *testing.T) {
	base := time.Now()
	previous := Snapshot{
		Info:       Info{Title: "Track", Artist: "Artist", Album: "Album", Status: "Playing", LengthSeconds: 120, PositionSeconds: 10},
		ReceivedAt: base,
	}
	next := Snapshot{
		Info:       Info{Title: "Track", Artist: "Artist", Album: "Album", Status: "Playing", LengthSeconds: 120, PositionSeconds: 10.48},
		ReceivedAt: base.Add(500 * time.Millisecond),
	}
	got := ReconcileSnapshot(previous, next, base.Add(500*time.Millisecond))
	if position := got.PositionAt(base.Add(500 * time.Millisecond)); math.Abs(position-10.5) > 0.001 {
		t.Fatalf("reconciled position = %.3f; want 10.5", position)
	}
}

func TestReconcileSnapshotAllowsClockCorrection(t *testing.T) {
	base := time.Now()
	previous := Snapshot{Info: Info{Title: "Track", Status: "Playing", PositionSeconds: 10}, ReceivedAt: base}
	next := Snapshot{Info: Info{Title: "Track", Status: "Playing", PositionSeconds: 10.1}, ReceivedAt: base.Add(500 * time.Millisecond)}
	if got := ReconcileSnapshot(previous, next, next.ReceivedAt); got.Info.PositionSeconds != 10.1 {
		t.Fatalf("authoritative clock correction suppressed: %#v", got)
	}
}

func TestReconcileSnapshotKeepsLargeSeekAndRepeatReset(t *testing.T) {
	base := time.Now()
	previous := Snapshot{
		Info:       Info{Title: "Track", Artist: "Artist", Album: "Album", Status: "Playing", LengthSeconds: 120, PositionSeconds: 40},
		ReceivedAt: base,
	}
	seek := Snapshot{
		Info:       Info{Title: "Track", Artist: "Artist", Album: "Album", Status: "Playing", LengthSeconds: 120, PositionSeconds: 10},
		ReceivedAt: base.Add(100 * time.Millisecond),
	}
	if got := ReconcileSnapshot(previous, seek, base.Add(100*time.Millisecond)); got.Info.PositionSeconds != 10 {
		t.Fatalf("large seek was smoothed: %#v", got.Info)
	}

	ending := Snapshot{
		Info:       Info{Title: "Track", Artist: "Artist", Album: "Album", Status: "Playing", LengthSeconds: 120, PositionSeconds: 119},
		ReceivedAt: base,
	}
	repeat := Snapshot{
		Info:       Info{Title: "Track", Artist: "Artist", Album: "Album", Status: "Playing", LengthSeconds: 120, PositionSeconds: 0},
		ReceivedAt: base.Add(100 * time.Millisecond),
	}
	if got := ReconcileSnapshot(ending, repeat, base.Add(100*time.Millisecond)); got.Info.PositionSeconds != 0 {
		t.Fatalf("repeat reset was smoothed: %#v", got.Info)
	}
}

func TestReconcileSnapshotDoesNotCrossTrackBoundary(t *testing.T) {
	base := time.Now()
	previous := Snapshot{
		Info:       Info{Title: "Old", Artist: "Artist", Status: "Playing", LengthSeconds: 120, PositionSeconds: 10},
		ReceivedAt: base,
	}
	next := Snapshot{
		Info:       Info{Title: "New", Artist: "Artist", Status: "Playing", LengthSeconds: 120, PositionSeconds: 0},
		ReceivedAt: base.Add(100 * time.Millisecond),
	}
	if got := ReconcileSnapshot(previous, next, base.Add(100*time.Millisecond)); got.Info.PositionSeconds != 0 {
		t.Fatalf("track change was smoothed: %#v", got.Info)
	}
}
