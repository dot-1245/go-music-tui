// Package lyrics contains lyric parsing, provider access, matching, and ranking.
package lyrics

import (
	"net/http"
	"time"

	"github.com/dot-1245/go-music-tui/internal/media"
)

// Logger is intentionally small so the application can connect its existing
// debug log without making the package depend on the UI or logging package.
type Logger func(format string, args ...interface{})

func logf(logger Logger, format string, args ...interface{}) {
	if logger != nil {
		logger(format, args...)
	}
}

// Client accesses the lyric providers using the supplied HTTP client.
type Client struct {
	HTTP             *http.Client
	Logger           Logger
	MaxResponseBytes int64
	CaptureBody      bool
}

// ClientOptions controls diagnostic response retention. The TUI only needs
// parsed results, while debug commands can explicitly retain response bodies.
type ClientOptions struct {
	MaxResponseBytes int64
	CaptureBody      bool
}

// NewClient creates a lyric provider client. A nil HTTP client gets a safe
// default timeout.
func NewClient(httpClient *http.Client, logger Logger) *Client {
	return NewClientWithOptions(httpClient, logger, ClientOptions{
		MaxResponseBytes: 4 << 20,
		CaptureBody:      true,
	})
}

// NewClientWithOptions creates a provider client with bounded response reads.
// A negative MaxResponseBytes disables the limit and should only be used by an
// explicit diagnostic command such as --raw.
func NewClientWithOptions(httpClient *http.Client, logger Logger, options ClientOptions) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if options.MaxResponseBytes == 0 {
		options.MaxResponseBytes = 4 << 20
	}
	return &Client{
		HTTP:             httpClient,
		Logger:           logger,
		MaxResponseBytes: options.MaxResponseBytes,
		CaptureBody:      options.CaptureBody,
	}
}

// Request describes a track lookup shared by all providers.
type Request struct {
	media.Track
	RawArtist string
}

// NewRequest builds a lookup request from common track metadata.
func NewRequest(title string, artists []string, rawArtist, album string, durationSec int) Request {
	return Request{
		Track: media.Track{
			Title:       title,
			Artists:     append([]string(nil), artists...),
			Artist:      rawArtist,
			Album:       album,
			DurationSec: durationSec,
		},
		RawArtist: rawArtist,
	}
}

// Result is the common representation returned by all lyric providers.
type Result struct {
	Title    string
	Artist   string
	Album    string
	Duration float64
	Lines    []Line
	Synced   string
	Plain    string
	Source   string
	Quality  int
}

// RequestRecord contains one provider HTTP exchange for diagnostics.
type RequestRecord struct {
	URL        string
	Purpose    string
	StatusCode int
	Body       []byte
	Err        error
}

// ProviderReport combines a provider result with the exchanges that produced
// it. Body retention is controlled by ClientOptions and is disabled for the
// runtime TUI client.
type ProviderReport struct {
	Provider string
	Result   *Result
	Requests []RequestRecord
}
