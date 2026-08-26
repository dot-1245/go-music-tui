package artwork

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestCalculatePlacement(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 50))
	placement := CalculatePlacement(img, 80, 24, false, PixelSize{})
	if placement.Width != 180 || placement.Row != 2 || placement.Column != 2 {
		t.Fatalf("unexpected compact placement: %#v", placement)
	}

	placement = CalculatePlacement(img, 80, 24, true, PixelSize{Width: 800, Height: 480, OK: true})
	if placement.Width == 0 || placement.Row < 1 || placement.Column < 1 {
		t.Fatalf("unexpected fullscreen placement: %#v", placement)
	}
}

func TestRender(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var output bytes.Buffer
	if err := Render(&output, img, 2); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte("\x1b_G")) {
		t.Fatalf("kitty payload has no graphics escape: %q", output.String()[:min(len(output.String()), 32)])
	}
}

func TestFetcherHTTPAndFile(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(encoded.Bytes())),
			Header:     make(http.Header),
		}, nil
	})}
	fetcher := NewFetcher(client)
	if _, err := fetcher.Fetch(context.Background(), "https://example.invalid/art.png"); err != nil {
		t.Fatalf("HTTP Fetch returned error: %v", err)
	}

	path := filepath.Join(t.TempDir(), "art.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fetcher.Fetch(context.Background(), "file://"+path); err != nil {
		t.Fatalf("file Fetch returned error: %v", err)
	}
	encodedPath := filepath.Join(t.TempDir(), "cover art.png")
	file, err = os.Create(encodedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	fileURL := (&url.URL{Scheme: "file", Path: encodedPath}).String()
	if _, err := fetcher.Fetch(context.Background(), fileURL); err != nil {
		t.Fatalf("escaped file URL Fetch returned error: %v", err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	if _, err := fetcher.Fetch(context.Background(), dataURL); err != nil {
		t.Fatalf("data URL Fetch returned error: %v", err)
	}
}

func TestFetcherRejectsOversizedResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(make([]byte, 32))),
			Header:     make(http.Header),
		}, nil
	})}
	fetcher := NewFetcher(client)
	fetcher.MaxBytes = 16
	if _, err := fetcher.Fetch(context.Background(), "https://example.invalid/large.png"); err == nil {
		t.Fatal("oversized response was accepted")
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	cache := NewCache(2)
	first := image.NewRGBA(image.Rect(0, 0, 1, 1))
	second := image.NewRGBA(image.Rect(0, 0, 2, 2))
	third := image.NewRGBA(image.Rect(0, 0, 3, 3))
	cache.Put("first", first)
	cache.Put("second", second)
	if _, ok := cache.Get("first"); !ok {
		t.Fatal("first cache entry was not found")
	}
	cache.Put("third", third)
	if _, ok := cache.Get("second"); ok {
		t.Fatal("least recently used entry was not evicted")
	}
	if _, ok := cache.Get("first"); !ok {
		t.Fatal("recently used entry was evicted")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
