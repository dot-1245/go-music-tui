// Package media contains metadata models shared by player, lyrics, and UI code.
package media

// Track is the provider-independent identity of a playing track.
type Track struct {
	Title       string
	Artist      string
	Artists     []string
	Album       string
	DurationSec int
}
