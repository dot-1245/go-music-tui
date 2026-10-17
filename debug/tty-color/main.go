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
	printSample("Text", theme.Text, theme.Reset, "body text / sung words")
	printSample("Primary", theme.Primary, theme.Reset, "title icon")
	printSample("Accent", theme.Accent, theme.Reset, "status icons / progress")
	printSample("SubText", theme.SubText, theme.Reset, "artist")
	printSample("Gray", theme.Gray, theme.Reset, "secondary text / upcoming words")

	fmt.Println("\nkaraoke sample:")
	fmt.Printf("%s歌い終わった %s%s歌っている%s %sこれから%s\n", theme.Text, theme.Bold, theme.Text, theme.BoldOff, theme.Gray, theme.Reset)
	if theme.Enabled() {
		fmt.Println("\nforeground comparison (normal / bold):")
		for _, code := range []int{39, 37, 97} {
			fmt.Printf("SGR %d: \x1b[%dm通常の歌詞 %s太字の歌詞%s\n", code, code, theme.Bold, theme.Reset)
		}
		fmt.Println("\nANSI 16 palette (normal / bold):")
		for index := 0; index < 16; index++ {
			code := 30 + index
			if index >= 8 {
				code = 90 + index - 8
			}
			fmt.Printf("color%-2d SGR %d: \x1b[%dmSample %sSample%s\n", index, code, code, theme.Bold, theme.Reset)
		}
	}

	if !theme.Enabled() {
		fmt.Println("\nANSI colors are disabled; the text above is intentionally plain.")
	}
}

func printSample(label, prefix, reset, sample string) {
	fmt.Printf("%-9s %s%s%s\n", label+":", prefix, sample, reset)
}
