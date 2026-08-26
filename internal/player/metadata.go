package player

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/dot-1245/go-music-tui/internal/media"
)

// Info is the MPRIS metadata and playback state consumed by the TUI.
type Info struct {
	Name, Title, Artist, Album, ArtUrl string
	Artists                            []string
	Status, Shuffle, Loop              string
	Volume, Position, Length           int
	PositionSeconds, LengthSeconds     float64
}

// Track returns the provider-independent identity represented by Info.
func (info Info) Track() media.Track {
	return media.Track{
		Title:       info.Title,
		Artist:      info.Artist,
		Artists:     append([]string(nil), info.Artists...),
		Album:       info.Album,
		DurationSec: info.Length,
	}
}

const (
	// C0 separators are accepted by playerctl as literal format text and are
	// much less likely to occur in metadata than the old `;;` separator.
	metadataFieldSeparator  = "\x1f"
	metadataRecordSeparator = "\x1e"
	metadataFormat          = "{{position}}\x1f{{mpris:length}}\x1f{{volume}}\x1f{{status}}\x1f{{xesam:title}}\x1f{{xesam:artist}}\x1f{{xesam:album}}\x1f{{mpris:artUrl}}\x1f{{shuffle}}\x1f{{loop}}\x1f{{playerName}}"
	metadataStreamFormat    = metadataFormat + metadataRecordSeparator
)

// List returns available player names in playerctl order.
func (c *Client) List(ctx context.Context) ([]string, error) {
	output, err := c.Output(ctx, "-l")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	players := make([]string, 0)
	for _, name := range strings.Fields(output) {
		if !seen[name] {
			seen[name] = true
			players = append(players, name)
		}
	}
	return players, nil
}

// Select chooses a preferred player, otherwise preferring a currently playing
// player over the first available name. It is intended for one-shot commands;
// the TUI uses Follow's %any event stream instead of repeatedly selecting.
func (c *Client) Select(ctx context.Context, preferred string) (string, []string, error) {
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		return preferred, []string{preferred}, nil
	}
	players, err := c.List(ctx)
	if err != nil {
		return "", nil, err
	}
	if len(players) == 0 {
		return "", players, nil
	}
	for _, name := range players {
		status, statusErr := c.Query(ctx, name, "status")
		if statusErr == nil && strings.EqualFold(strings.TrimSpace(status), "playing") {
			return name, players, nil
		}
	}
	return players[0], players, nil
}

// Artists returns xesam:artist values as individual lines when the player
// exposes them that way.
func (c *Client) Artists(ctx context.Context, playerName string) ([]string, error) {
	output, err := c.Query(ctx, playerName, "metadata", "xesam:artist")
	if err != nil {
		return nil, err
	}
	var artists []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		artists = append(artists, line)
	}
	return artists, nil
}

// Metadata reads the same fields used by the main TUI.
func (c *Client) Metadata(ctx context.Context, playerName string) (Info, error) {
	output, err := c.Query(ctx, playerName, "metadata", "--format", metadataFormat)
	if err != nil {
		return Info{}, err
	}
	return ParseMetadata(output, playerName)
}

// ParseMetadata parses the format emitted by Metadata and Follow. It keeps
// textual metadata usable even when an optional numeric property is missing or
// malformed; only a structurally incomplete response is fatal.
func ParseMetadata(output, fallbackPlayerName string) (Info, error) {
	output = strings.TrimSuffix(output, metadataRecordSeparator)
	parts := strings.Split(output, metadataFieldSeparator)
	newFormat := strings.Contains(output, metadataFieldSeparator)
	if newFormat && len(parts) != 11 {
		return Info{}, fmt.Errorf("playerctl metadata returned %d fields with the safe separator, want 11", len(parts))
	}
	if !newFormat {
		// Accept the format used by older debug fixtures and older local builds.
		parts = strings.Split(output, ";;")
	}
	if len(parts) < 10 {
		return Info{}, fmt.Errorf("playerctl metadata returned %d fields, want at least 10", len(parts))
	}

	position := parseMicroseconds(parts[0])
	lengthSeconds := parseMicroseconds(parts[1])
	volume := parseVolume(parts[2])
	playerName := fallbackPlayerName
	if len(parts) > 10 && strings.TrimSpace(parts[10]) != "" {
		playerName = strings.TrimSpace(parts[10])
	}
	if lengthSeconds > float64(math.MaxInt) {
		lengthSeconds = float64(math.MaxInt)
	}
	info := Info{
		Name:            playerName,
		Position:        int(position),
		PositionSeconds: position,
		Length:          int(lengthSeconds),
		LengthSeconds:   lengthSeconds,
		Volume:          volume,
		Status:          strings.TrimSpace(parts[3]),
		Title:           strings.TrimSpace(parts[4]),
		Artist:          strings.TrimSpace(parts[5]),
		Album:           strings.TrimSpace(parts[6]),
		ArtUrl:          strings.TrimSpace(parts[7]),
		Shuffle:         strings.TrimSpace(parts[8]),
		Loop:            strings.TrimSpace(parts[9]),
	}
	info.Artists = FlattenArtists(SplitArtistsFallback(info.Artist))
	return info, nil
}

func parseMicroseconds(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed <= 0 {
		return 0
	}
	return parsed / 1_000_000
}

func parseVolume(value string) int {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0
	}
	if parsed <= 1 {
		parsed *= 100
	}
	if parsed < 0 {
		return 0
	}
	if parsed > 100 {
		return 100
	}
	return int(parsed)
}

var artistSplitRe = regexp.MustCompile(`\s*[,;]\s*|\s+/\s+|\s+(?:feat\.?|ft\.?|with|&)\s+`)

// SplitArtistsFallback splits a combined artist value from players that do
// not expose the MPRIS array as separate lines.
func SplitArtistsFallback(joined string) []string {
	if strings.TrimSpace(joined) == "" {
		return nil
	}
	parts := artistSplitRe.Split(joined, -1)
	artists := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			artists = append(artists, part)
		}
	}
	return artists
}

// FlattenArtists splits combined values and removes duplicates while retaining
// the first-seen order.
func FlattenArtists(artists []string) []string {
	flattened := make([]string, 0, len(artists))
	seen := make(map[string]bool)
	for _, artist := range artists {
		parts := SplitArtistsFallback(artist)
		if len(parts) == 0 {
			parts = []string{artist}
		}
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" && !seen[part] {
				seen[part] = true
				flattened = append(flattened, part)
			}
		}
	}
	return flattened
}
