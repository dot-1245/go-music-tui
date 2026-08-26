package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/dot-1245/go-music-tui/internal/artwork"
)

func main() {
	source := flag.String("source", "", "image URL or local/file path")
	cols := flag.Int("cols", 80, "terminal columns used for placement")
	rows := flag.Int("rows", 24, "terminal rows used for placement")
	pixelWidth := flag.Int("pixel-width", 0, "terminal pixel width; omit when unavailable")
	pixelHeight := flag.Int("pixel-height", 0, "terminal pixel height; omit when unavailable")
	fullScreen := flag.Bool("fullscreen", false, "calculate centered fullscreen placement")
	render := flag.Bool("render", false, "write kitty graphics escape sequences to stdout")
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
	if *cols < 1 || *rows < 1 {
		fmt.Fprintln(os.Stderr, "-cols and -rows must be positive")
		os.Exit(2)
	}
	if (*pixelWidth == 0) != (*pixelHeight == 0) || *pixelWidth < 0 || *pixelHeight < 0 {
		fmt.Fprintln(os.Stderr, "-pixel-width and -pixel-height must be supplied together as non-negative values")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	img, err := artwork.NewFetcher(&http.Client{Timeout: *timeout}).Fetch(ctx, *source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "artwork fetch failed: %v\n", err)
		os.Exit(1)
	}

	pixels := artwork.PixelSize{}
	if *pixelWidth > 0 && *pixelHeight > 0 {
		pixels = artwork.PixelSize{Width: *pixelWidth, Height: *pixelHeight, OK: true}
	} else {
		pixels = artwork.GetTermPixelSize(os.Stdout)
	}
	placement := artwork.CalculatePlacement(img, *cols, *rows, *fullScreen, pixels)
	bounds := img.Bounds()
	fmt.Printf("image: %dx%d\n", bounds.Dx(), bounds.Dy())
	fmt.Printf("terminal: %dx%d cells", *cols, *rows)
	if pixels.OK {
		fmt.Printf(" (%dx%d pixels)", pixels.Width, pixels.Height)
	}
	fmt.Println()
	fmt.Printf("placement: row=%d column=%d width=%d pixels\n", placement.Row, placement.Column, placement.Width)

	if !*render {
		return
	}
	fmt.Fprintf(os.Stderr, "rendering kitty graphics payload to stdout\n")
	fmt.Printf("\033[%d;%dH", placement.Row, placement.Column)
	if err := artwork.Render(os.Stdout, img, placement.Width); err != nil {
		fmt.Fprintf(os.Stderr, "artwork render failed: %v\n", err)
		os.Exit(1)
	}
}
