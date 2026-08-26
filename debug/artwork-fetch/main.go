package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"net/http"
	"os"
	"time"

	"github.com/dot-1245/go-music-tui/internal/artwork"
)

func main() {
	source := flag.String("source", "", "image URL or local/file path")
	out := flag.String("out", "", "optional path to save the decoded image as PNG")
	timeout := flag.Duration("timeout", 15*time.Second, "image request timeout")
	flag.Parse()
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "-timeout must be positive")
		os.Exit(2)
	}

	if *source == "" {
		fmt.Fprintln(os.Stderr, "-source is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	img, err := artwork.NewFetcher(&http.Client{Timeout: *timeout}).Fetch(ctx, *source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "artwork fetch failed: %v\n", err)
		os.Exit(1)
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	aspect := 0.0
	if height > 0 {
		aspect = float64(width) / float64(height)
	}
	fmt.Printf("source: %q\n", *source)
	fmt.Printf("image: %dx%d aspect=%.3f\n", width, height, aspect)

	if *out == "" {
		return
	}
	file, err := os.Create(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create output: %v\n", err)
		os.Exit(1)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		fmt.Fprintf(os.Stderr, "encode output: %v\n", err)
		os.Exit(1)
	}
	if err := file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close output: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("saved: %q\n", *out)
}
