package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dot-1245/go-music-tui/internal/debugutil"
	"github.com/dot-1245/go-music-tui/internal/lyrics"
	"github.com/dot-1245/go-music-tui/internal/player"
)

type resultSummary struct {
	Source  string  `json:"source"`
	Title   string  `json:"title"`
	Artist  string  `json:"artist"`
	Album   string  `json:"album"`
	Quality int     `json:"quality"`
	Lines   int     `json:"lines"`
	Words   int     `json:"words"`
	Score   float64 `json:"score"`
}

type requestSummary struct {
	URL        string `json:"url"`
	Purpose    string `json:"purpose,omitempty"`
	StatusCode int    `json:"status_code"`
	Error      string `json:"error,omitempty"`
	Preview    string `json:"preview,omitempty"`
}

type reportSummary struct {
	Provider string           `json:"provider"`
	Requests int              `json:"requests"`
	Result   *resultSummary   `json:"result,omitempty"`
	HTTP     []requestSummary `json:"http,omitempty"`
}

func main() {
	title := flag.String("title", "", "track title")
	artist := flag.String("artist", "", "artist name; player-style separators are accepted")
	rawArtist := flag.String("raw-artist", "", "raw artist value sent to providers")
	album := flag.String("album", "", "album name")
	duration := flag.Int("duration", 0, "track duration in seconds")
	providerName := flag.String("provider", "all", "provider: all, lrclib, synclrc, or amll")
	timeout := flag.Duration("timeout", 15*time.Second, "request timeout")
	verbose := flag.Bool("v", false, "show request details and larger response previews")
	raw := flag.Bool("raw", false, "write the selected provider's complete final response to stdout")
	jsonOutput := flag.Bool("json", false, "write a machine-readable summary")
	flag.Parse()
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "-timeout must be positive")
		os.Exit(2)
	}

	if strings.TrimSpace(*title) == "" {
		fmt.Fprintln(os.Stderr, "-title is required")
		os.Exit(2)
	}
	provider := strings.ToLower(strings.TrimSpace(*providerName))
	if provider != "all" && provider != "lrclib" && provider != "synclrc" && provider != "amll" {
		fmt.Fprintf(os.Stderr, "unsupported provider %q (want all, lrclib, synclrc, or amll)\n", *providerName)
		os.Exit(2)
	}
	if *raw && provider == "all" {
		fmt.Fprintln(os.Stderr, "--raw requires --provider to select one provider")
		os.Exit(2)
	}
	if *raw && *jsonOutput {
		fmt.Fprintln(os.Stderr, "--raw and --json cannot be used together")
		os.Exit(2)
	}

	artists := player.SplitArtistsFallback(*artist)
	if len(artists) == 0 && strings.TrimSpace(*artist) != "" {
		artists = []string{strings.TrimSpace(*artist)}
	}
	if strings.TrimSpace(*rawArtist) == "" {
		*rawArtist = strings.Join(artists, "; ")
	}
	request := lyrics.NewRequest(*title, artists, *rawArtist, *album, *duration)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	maxResponseBytes := int64(4 << 20)
	if *raw {
		// --raw is an explicit diagnostic escape hatch. Normal TUI/debug paths
		// remain bounded so a broken endpoint cannot consume unbounded memory.
		maxResponseBytes = -1
	}
	client := lyrics.NewClientWithOptions(&http.Client{Timeout: *timeout}, nil, lyrics.ClientOptions{
		MaxResponseBytes: maxResponseBytes,
		CaptureBody:      true,
	})

	var reports []lyrics.ProviderReport
	if provider == "all" {
		reports = client.FetchAll(ctx, request)
	} else {
		reports = []lyrics.ProviderReport{client.FetchProvider(ctx, provider, request)}
	}

	if *raw {
		if len(reports) == 0 {
			fmt.Fprintln(os.Stderr, "provider returned no report")
			os.Exit(1)
		}
		record := rawRecord(reports[0].Requests)
		if record == nil {
			fmt.Fprintln(os.Stderr, "provider made no HTTP request")
			os.Exit(1)
		}
		if len(record.Body) == 0 && record.Err != nil {
			fmt.Fprintf(os.Stderr, "provider request failed: %v\n", record.Err)
			os.Exit(1)
		}
		if _, err := os.Stdout.Write(record.Body); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *jsonOutput {
		output := make([]reportSummary, 0, len(reports))
		for _, report := range reports {
			output = append(output, summarize(report, request, *verbose))
		}
		if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("track: %q\n", request.Title)
	fmt.Printf("artists: %v\n", request.Artists)
	fmt.Printf("album: %q duration: %ds\n", request.Album, request.DurationSec)
	for _, report := range reports {
		printReport(report, request, *verbose)
	}
}

func lastRecord(records []lyrics.RequestRecord) *lyrics.RequestRecord {
	for i := len(records) - 1; i >= 0; i-- {
		if len(records[i].Body) > 0 || records[i].Err != nil || records[i].StatusCode != 0 {
			record := records[i]
			return &record
		}
	}
	return nil
}

func rawRecord(records []lyrics.RequestRecord) *lyrics.RequestRecord {
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if record.Purpose == "lyrics" && len(record.Body) > 0 {
			return &record
		}
	}
	return lastRecord(records)
}

func resultSummaryFor(result *lyrics.Result, request lyrics.Request) *resultSummary {
	if result == nil {
		return nil
	}
	return &resultSummary{
		Source:  result.Source,
		Title:   result.Title,
		Artist:  result.Artist,
		Album:   result.Album,
		Quality: result.Quality,
		Lines:   len(result.Lines),
		Words:   lyrics.CountWords(result.Lines),
		Score:   lyrics.ResultMatchScore(result, request.Title, request.Artists, request.Album),
	}
}

func summarize(report lyrics.ProviderReport, request lyrics.Request, verbose bool) reportSummary {
	summary := reportSummary{
		Provider: report.Provider,
		Requests: len(report.Requests),
		Result:   resultSummaryFor(report.Result, request),
	}
	if verbose {
		for _, record := range report.Requests {
			item := requestSummary{URL: record.URL, Purpose: record.Purpose, StatusCode: record.StatusCode, Preview: debugutil.Preview(record.Body, 4096)}
			if record.Err != nil {
				item.Error = record.Err.Error()
			}
			summary.HTTP = append(summary.HTTP, item)
		}
	}
	return summary
}

func printReport(report lyrics.ProviderReport, request lyrics.Request, verbose bool) {
	fmt.Printf("\n[%s]\n", report.Provider)
	fmt.Printf("requests: %d\n", len(report.Requests))
	if report.Result == nil {
		fmt.Println("result: none")
	} else {
		result := resultSummaryFor(report.Result, request)
		fmt.Printf("result: source=%s quality=%d score=%.2f lines=%d words=%d\n", result.Source, result.Quality, result.Score, result.Lines, result.Words)
		fmt.Printf("metadata: title=%q artist=%q album=%q\n", result.Title, result.Artist, result.Album)
	}

	if verbose {
		for i, record := range report.Requests {
			fmt.Printf("request[%d]: purpose=%s status=%d url=%s\n", i, record.Purpose, record.StatusCode, record.URL)
			if record.Err != nil {
				fmt.Printf("error[%d]: %v\n", i, record.Err)
			}
			fmt.Printf("raw preview[%d]: %s\n", i, debugutil.Preview(record.Body, 4096))
		}
		return
	}
	if record := lastRecord(report.Requests); record != nil {
		fmt.Printf("last request: purpose=%s status=%d url=%s\n", record.Purpose, record.StatusCode, record.URL)
		if record.Err != nil {
			fmt.Printf("error: %v\n", record.Err)
		}
		fmt.Printf("raw preview: %s\n", debugutil.Preview(record.Body, 256))
	}
}
