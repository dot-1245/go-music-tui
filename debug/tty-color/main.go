package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dot-1245/go-music-tui/internal/ttycolor"
)

func main() {
	modeValue := flag.String("mode", string(ttycolor.ModeAuto), "color mode: auto, always, or never")
	flag.Parse()

	mode, err := ttycolor.ParseMode(*modeValue)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	theme := ttycolor.New(mode, os.Stdout)

	fmt.Println("go-music-tui tty-color debug")
	fmt.Printf("mode: %s\n", mode)
	fmt.Printf("stdout is TTY: %t\n", ttycolor.IsTTY(os.Stdout))
	fmt.Printf("TERM: %q\n", os.Getenv("TERM"))
	fmt.Printf("COLORTERM: %q\n", os.Getenv("COLORTERM"))
	fmt.Printf("NO_COLOR: %q\n", os.Getenv("NO_COLOR"))
	fmt.Printf("colors enabled: %t\n\n", theme.Enabled())

	fmt.Println("semantic palette:")
	printSample("Primary", theme.Primary, theme.Reset, "title / current line")
	printSample("Accent", theme.Accent, theme.Reset, "status / progress / sung words")
	printSample("SubText", theme.SubText, theme.Reset, "artist")
	printSample("Gray", theme.Gray, theme.Reset, "secondary text / upcoming words")

	fmt.Println("\nkaraoke sample:")
	fmt.Printf("%s歌い終わった %s%s歌っている%s %sこれから%s\n", theme.Accent, theme.Bold, theme.Primary, theme.BoldOff, theme.Gray, theme.Reset)

	if !theme.Enabled() {
		fmt.Println("\nANSI colors are disabled; the text above is intentionally plain.")
	}
}

func printSample(label, prefix, reset, sample string) {
	fmt.Printf("%-9s %s%s%s\n", label+":", prefix, sample, reset)
}
