package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/dot-1245/go-music-tui/internal/player"
)

func main() {
	playerName := flag.String("player", "", "playerctl player name; defaults to the first active player")
	actionName := flag.String("action", "", "action: play-pause, previous, next, volume-up, volume-down, seek-back, seek-forward, shuffle, loop")
	execute := flag.Bool("execute", false, "actually change playback; without this flag only print the command")
	loopState := flag.String("loop-state", "None", "loop state used for dry-run: None, Track, or Playlist")
	timeout := flag.Duration("timeout", 3*time.Second, "playerctl command timeout")
	flag.Parse()
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "-timeout must be positive")
		os.Exit(2)
	}

	action := *actionName
	args, ok := player.ActionArgs(action)
	if !ok {
		fmt.Fprintln(os.Stderr, "an action is required: play-pause, previous, next, volume-up, volume-down, seek-back, seek-forward, shuffle, or loop")
		os.Exit(2)
	}
	if action == "loop" && *loopState != "None" && *loopState != "Track" && *loopState != "Playlist" {
		fmt.Fprintln(os.Stderr, "-loop-state must be None, Track, or Playlist")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := player.NewWithTimeout(nil, *timeout)
	selected := *playerName
	if !*execute && selected == "" {
		selected = "%any"
	}
	if selected == "" {
		var selectionErr error
		selected, _, selectionErr = client.Select(ctx, "")
		if selectionErr != nil {
			fmt.Fprintf(os.Stderr, "player selection failed: %v\n", selectionErr)
			os.Exit(1)
		}
		if selected == "" {
			fmt.Fprintln(os.Stderr, "no player found")
			os.Exit(1)
		}
	}
	if action == "loop" {
		current := *loopState
		if *execute {
			var queryErr error
			current, queryErr = client.Query(ctx, selected, "loop")
			if queryErr != nil {
				fmt.Fprintf(os.Stderr, "loop query failed: %v\n", queryErr)
				os.Exit(1)
			}
		}
		args = []string{"loop", player.NextLoop(current)}
	}

	command := append([]string{"playerctl", "-p", selected}, args...)
	fmt.Printf("command: %q\n", command)
	if !*execute {
		fmt.Println("dry-run: playback was not changed; pass --execute to run it")
		return
	}
	if err := client.Control(ctx, selected, args...); err != nil {
		fmt.Fprintf(os.Stderr, "control failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("control succeeded")
}
