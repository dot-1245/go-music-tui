package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/dot-1245/go-music-tui/internal/player"
)

type metadataOutput struct {
	Info    player.Info `json:"info"`
	Artists []string    `json:"artists,omitempty"`
}

func main() {
	playerName := flag.String("player", "", "playerctl player name; defaults to the first active player")
	timeout := flag.Duration("timeout", 3*time.Second, "playerctl command timeout")
	jsonOutput := flag.Bool("json", false, "write JSON")
	flag.Parse()
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "-timeout must be positive")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := player.NewWithTimeout(nil, *timeout)
	selected, players, err := client.Select(ctx, *playerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "player selection failed: %v\n", err)
		os.Exit(1)
	}
	if selected == "" {
		fmt.Fprintln(os.Stderr, "no player found")
		os.Exit(1)
	}
	info, err := client.Metadata(ctx, selected)
	if err != nil {
		fmt.Fprintf(os.Stderr, "metadata failed for %q: %v\n", selected, err)
		os.Exit(1)
	}
	artists, artistErr := client.Artists(ctx, selected)
	if artistErr != nil {
		fmt.Fprintf(os.Stderr, "artist query failed for %q: %v\n", selected, artistErr)
	}

	if *jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(metadataOutput{Info: info, Artists: artists}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("players: %v\n", players)
	fmt.Printf("player: %s\n", info.Name)
	fmt.Printf("status: %s\n", info.Status)
	fmt.Printf("title: %q\n", info.Title)
	fmt.Printf("artist: %q\n", info.Artist)
	fmt.Printf("artists: %v\n", artists)
	fmt.Printf("album: %q\n", info.Album)
	fmt.Printf("position: %ds / %ds\n", info.Position, info.Length)
	fmt.Printf("volume: %d%%\n", info.Volume)
	fmt.Printf("shuffle: %s\n", info.Shuffle)
	fmt.Printf("loop: %s\n", info.Loop)
	fmt.Printf("art URL: %q\n", info.ArtUrl)
}
