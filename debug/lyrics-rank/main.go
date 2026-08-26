package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dot-1245/go-music-tui/internal/lyrics"
)

func main() {
	fileName := flag.String("file", "", "JSON file containing an array of provider candidates")
	title := flag.String("title", "Test Track", "target title")
	artist := flag.String("artist", "Test Artist", "target artist")
	album := flag.String("album", "Test Album", "target album")
	duration := flag.Int("duration", 120, "target duration in seconds")
	flag.Parse()

	candidates, err := loadCandidates(*fileName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	targetArtists := []string{*artist}
	validResults := make([]struct {
		index  int
		result *lyrics.Result
	}, 0, len(candidates))
	for i, candidate := range candidates {
		result := lyrics.ResultFromMap(candidate, "debug", 390)
		if result == nil {
			fmt.Printf("[%d] rejected: no parseable synchronized lyrics\n", i)
			continue
		}
		score := lyrics.ResultMatchScore(result, *title, targetArtists, *album)
		fmt.Printf("[%d] title=%q artist=%q duration=%.0fs quality=%d lines=%d words=%d score=%.2f\n", i, result.Title, result.Artist, result.Duration, result.Quality, len(result.Lines), lyrics.CountWords(result.Lines), score)
		validResults = append(validResults, struct {
			index  int
			result *lyrics.Result
		}{index: i, result: result})
	}

	best := lyrics.PickBestMatch(candidates, *duration, *title, targetArtists, func(format string, args ...interface{}) {
		fmt.Printf("  detail: %s\n", strings.TrimSpace(fmt.Sprintf(format, args...)))
	})
	if best == nil {
		fmt.Println("search selected: none")
	} else {
		encoded, _ := json.Marshal(best)
		fmt.Printf("search selected: %s\n", encoded)
	}

	var providerBest *lyrics.Result
	providerBestIndex := -1
	for _, candidate := range validResults {
		if lyrics.BetterResult(candidate.result, providerBest, *duration, *title, targetArtists, *album) {
			providerBest = candidate.result
			providerBestIndex = candidate.index
		}
	}
	if providerBest == nil {
		fmt.Println("provider selected: none")
		return
	}
	fmt.Printf("provider selected: index=%d source=%s quality=%d duration=%.0fs words=%d\n", providerBestIndex, providerBest.Source, providerBest.Quality, providerBest.Duration, lyrics.CountWords(providerBest.Lines))
}

func loadCandidates(fileName string) ([]map[string]interface{}, error) {
	if fileName == "" {
		return []map[string]interface{}{
			{"trackName": "Test Track", "artistName": "Test Artist", "albumName": "Test Album", "duration": 120.0, "syncedLyrics": "[00:00]ordinary"},
			{"trackName": "Test Track", "artistName": "Test Artist", "albumName": "Test Album", "duration": 121.0, "syncedLyrics": "[00:00]<00:00>karaoke"},
			{"trackName": "Unrelated", "artistName": "Other Artist", "duration": 120.0, "syncedLyrics": "[00:00]wrong"},
		}, nil
	}
	data, err := os.ReadFile(fileName)
	if err != nil {
		return nil, err
	}
	var candidates []map[string]interface{}
	if err := json.Unmarshal(data, &candidates); err != nil {
		return nil, err
	}
	return candidates, nil
}
