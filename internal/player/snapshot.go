package player

import (
	"strings"
	"time"
)

// Snapshot is a player state captured at ReceivedAt. The timestamp is used to
// advance a playing position locally between playerctl notifications.
type Snapshot struct {
	Info       Info
	ReceivedAt time.Time
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

// TrackKey identifies changes that require lyric and artwork work.
func (snapshot Snapshot) TrackKey() string {
	info := snapshot.Info
	return strings.Join([]string{info.Name, info.Title, info.Artist, info.Album, info.ArtUrl}, "\x00")
}
