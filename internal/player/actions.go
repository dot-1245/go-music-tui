package player

// ActionArgs translates a debug/control action into playerctl arguments.
func ActionArgs(action string) ([]string, bool) {
	switch action {
	case "play-pause", "previous", "next":
		return []string{action}, true
	case "volume-up":
		return []string{"volume", "0.05+"}, true
	case "volume-down":
		return []string{"volume", "0.05-"}, true
	case "seek-back":
		return []string{"position", "5-"}, true
	case "seek-forward":
		return []string{"position", "5+"}, true
	case "shuffle":
		return []string{"shuffle", "Toggle"}, true
	case "loop":
		return nil, true
	default:
		return nil, false
	}
}

// NextLoop returns the next MPRIS loop mode used by the keyboard and debug
// control paths.
func NextLoop(current string) string {
	switch current {
	case "None", "":
		return "Track"
	case "Track":
		return "Playlist"
	default:
		return "None"
	}
}
