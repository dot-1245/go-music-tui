package player

import (
	"math"
	"strings"
	"time"
)

// Snapshot is a player state captured at ReceivedAt. The timestamp is used to
// advance a playing position locally between playerctl notifications.
type Snapshot struct {
	Info       Info
	ReceivedAt time.Time
}

const positionBackstepTolerance = 0.05

// ReconcileSnapshot removes small backward position jumps caused by a
// playerctl event being delivered after the position it contains was sampled.
// Those jumps are especially visible when a word or line boundary is crossed:
// the local clock reaches the boundary, then a stale event moves it back and
// the renderer appears to wait for the same boundary again. Only jitter within
// one frame (50ms) is suppressed; larger corrections, seeks and repeats pass
// through so an extrapolated clock cannot stay substantially ahead.
func ReconcileSnapshot(previous, next Snapshot, now time.Time) Snapshot {
	if !sameTrack(previous.Info, next.Info) || !isPlaying(previous.Info) || !isPlaying(next.Info) {
		return next
	}
	if previous.AtTrackEnd(now) {
		// A repeated track can keep the same metadata while its position jumps
		// from the end back to zero. Do not smooth that legitimate reset.
		return next
	}

	previousPosition := previous.PositionAt(now)
	nextPosition := next.PositionAt(now)
	backstep := previousPosition - nextPosition
	if backstep <= 0 || backstep > positionBackstepTolerance {
		return next
	}

	next.Info.PositionSeconds = previousPosition
	if previousPosition >= float64(math.MaxInt) {
		next.Info.Position = math.MaxInt
	} else {
		next.Info.Position = int(previousPosition)
	}
	next.ReceivedAt = now
	return next
}

func isPlaying(info Info) bool {
	return strings.EqualFold(strings.TrimSpace(info.Status), "playing")
}

func sameTrack(left, right Info) bool {
	if left.Title == "" && left.Artist == "" && left.Album == "" && right.Title == "" && right.Artist == "" && right.Album == "" {
		return false
	}
	return left.Title == right.Title && left.Artist == right.Artist && left.Album == right.Album &&
		(left.LengthSeconds == 0 || right.LengthSeconds == 0 || left.LengthSeconds == right.LengthSeconds)
}

// PositionAt returns the best current estimate of the playback position.
// MPRIS does not require a continuous position signal, so a playing snapshot
// is extrapolated using the monotonic component of time.Time.
func (snapshot Snapshot) PositionAt(now time.Time) float64 {
	position := snapshot.Info.PositionSeconds
	if position == 0 && snapshot.Info.Position > 0 {
		// Position is kept for compatibility with callers that only populate the
		// integer field. Parsed playerctl snapshots also carry the precise value.
		position = float64(snapshot.Info.Position)
	}
	if strings.EqualFold(strings.TrimSpace(snapshot.Info.Status), "playing") && !snapshot.ReceivedAt.IsZero() {
		if elapsed := now.Sub(snapshot.ReceivedAt).Seconds(); elapsed > 0 {
			position += elapsed
		}
	}
	if position < 0 {
		return 0
	}
	length := snapshot.Info.LengthSeconds
	if length <= 0 {
		length = float64(snapshot.Info.Length)
	}
	if length > 0 && position > length {
		return length
	}
	return position
}

// AtTrackEnd reports whether a playing snapshot has reached its known end.
// Position is not a PropertiesChanged property in MPRIS, so callers can use
// this as a low-frequency recovery point for players that do not emit a
// metadata event when repeating the same track.
func (snapshot Snapshot) AtTrackEnd(now time.Time) bool {
	if !strings.EqualFold(strings.TrimSpace(snapshot.Info.Status), "playing") {
		return false
	}
	length := snapshot.Info.LengthSeconds
	if length <= 0 {
		length = float64(snapshot.Info.Length)
	}
	return length > 0 && snapshot.PositionAt(now) >= length
}

// TrackKey identifies changes that require lyric and artwork work.
func (snapshot Snapshot) TrackKey() string {
	info := snapshot.Info
	return strings.Join([]string{info.Name, info.Title, info.Artist, info.Album, info.ArtUrl}, "\x00")
}
