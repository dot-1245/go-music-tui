// Package artwork contains album-art fetching, sizing, and kitty rendering.
package artwork

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Fetcher reads images from HTTP(S) URLs or local/file URLs.
type Fetcher struct {
	HTTP      *http.Client
	MaxBytes  int64
	MaxPixels int64
}

const (
	defaultMaxBytes  = 16 << 20
	defaultMaxPixels = 20_000_000
)

// NewFetcher creates an image fetcher with a bounded default timeout.
func NewFetcher(client *http.Client) *Fetcher {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Fetcher{HTTP: client, MaxBytes: defaultMaxBytes, MaxPixels: defaultMaxPixels}
}

// Fetch decodes an image source supplied by MPRIS or a debug command.
func (f *Fetcher) Fetch(ctx context.Context, source string) (image.Image, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("empty artwork source")
	}
	if strings.HasPrefix(strings.ToLower(source), "data:") {
		data, err := decodeDataURL(source, f.MaxBytes)
		if err != nil {
			return nil, err
		}
		return f.decode(bytes.NewReader(data))
	}
	if strings.HasPrefix(strings.ToLower(source), "http://") || strings.HasPrefix(strings.ToLower(source), "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, err
		}
		client := f.HTTP
		if client == nil {
			client = &http.Client{Timeout: 15 * time.Second}
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("artwork request returned HTTP %d", resp.StatusCode)
		}
		if f.MaxBytes > 0 && resp.ContentLength > f.MaxBytes {
			return nil, fmt.Errorf("artwork response is too large: %d bytes", resp.ContentLength)
		}
		return f.decode(resp.Body)
	}

	path, err := localPath(source)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return f.decode(file)
}

func (f *Fetcher) decode(reader io.Reader) (image.Image, error) {
	if reader == nil {
		return nil, fmt.Errorf("nil artwork reader")
	}
	maxBytes := f.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	limited := io.LimitReader(reader, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("artwork exceeds %d-byte limit", maxBytes)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	maxPixels := f.MaxPixels
	if maxPixels <= 0 {
		maxPixels = defaultMaxPixels
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > maxPixels/int64(config.Height) {
		return nil, fmt.Errorf("artwork dimensions are too large: %dx%d", config.Width, config.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func localPath(source string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(source), "file:") {
		if parsed, err := url.Parse(source); err == nil && parsed.Scheme != "" {
			return "", fmt.Errorf("unsupported artwork URL scheme %q", parsed.Scheme)
		}
		return source, nil
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return "", err
	}
	if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
		return "", fmt.Errorf("unsupported file URL host %q", parsed.Host)
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("empty file URL path")
	}
	return path, nil
}

func decodeDataURL(source string, maxBytes int64) ([]byte, error) {
	comma := strings.IndexByte(source, ',')
	if comma < 0 {
		return nil, fmt.Errorf("invalid data artwork URL")
	}
	metadata, value := source[:comma], source[comma+1:]
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	if strings.HasSuffix(strings.ToLower(metadata), ";base64") {
		if int64(len(value)) > (maxBytes/3)*4+4 {
			return nil, fmt.Errorf("artwork data URL exceeds %d-byte limit", maxBytes)
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	}
	if int64(len(value)) > maxBytes {
		return nil, fmt.Errorf("artwork data URL exceeds %d-byte limit", maxBytes)
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return nil, err
	}
	return []byte(decoded), nil
}
