package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/dot-1245/go-music-tui/internal/artwork"
	"github.com/dot-1245/go-music-tui/internal/lyricsview"
	"github.com/dot-1245/go-music-tui/internal/player"
	"github.com/dot-1245/go-music-tui/internal/ttycolor"
)

type frameOptions struct {
	NoInfo, NoLyrics, NoArt bool
	ArtOnly, LyricsOnly     bool
}

// buildFrame assembles one complete terminal frame in memory. One write per
// frame prevents partial ANSI sequences and reduces output-side jitter.
func buildFrame(
	buffer *bytes.Buffer,
	snapshot player.Snapshot,
	hasPlayer bool,
	now time.Time,
	cols, rows int,
	terminal *os.File,
	theme ttycolor.Theme,
	lyricsSnapshot []LyricLine,
	artSnapshot artworkSnapshot,
	redrawArtwork bool,
	options frameOptions,
) error {
	wipeTextArea(buffer, cols, rows, options)
	if redrawArtwork && !options.NoArt {
		buffer.WriteString("\x1b_Ga=d\x1b\\")
		if artSnapshot.Image != nil && artSnapshot.Source != "" {
			pixels := artwork.GetTermPixelSize(terminal)
			placement := artwork.CalculatePlacement(artSnapshot.Image, cols, rows, options.ArtOnly, pixels)
			fmt.Fprintf(buffer, "\033[%d;%dH", placement.Row, placement.Column)
			if err := artwork.Render(buffer, artSnapshot.Image, placement.Width); err != nil {
				// Keep rendering the text frame even if the terminal graphics
				// encoder rejects one image.
				return appendTextFrame(buffer, snapshot, hasPlayer, now, cols, rows, theme, lyricsSnapshot, options, err)
			}
		}
	}
	return appendTextFrame(buffer, snapshot, hasPlayer, now, cols, rows, theme, lyricsSnapshot, options, nil)
}

func appendTextFrame(
	buffer *bytes.Buffer,
	snapshot player.Snapshot,
	hasPlayer bool,
	now time.Time,
	cols, rows int,
	theme ttycolor.Theme,
	lyricsSnapshot []LyricLine,
	options frameOptions,
	artErr error,
) error {
	if !hasPlayer {
		fmt.Fprintf(buffer, "\033[H\033[K %s󰝛 No player found.%s", theme.Gray, theme.Reset)
		return artErr
	}

	info := snapshot.Info
	position := snapshot.PositionAt(now)
	offsetX := textOffset(cols, rows, options)

	drawLine := func(y int, color, icon, label, text string, sanitize bool) {
		if y < 1 || rows > 0 && y > rows {
			return
		}
		if sanitize {
			text = lyricsview.SafeText(text)
			limit := cols - offsetX - 10
			if limit > 0 {
				text = truncateRunes(text, limit)
			} else {
				text = ""
			}
		}
		fmt.Fprintf(buffer, "\033[%d;%dH%s%s %s%-8s: %s%s\033[K", y, offsetX, color, icon, theme.Gray, label, theme.Reset, text)
	}
	draw := func(y int, color, icon, label, text string) {
		drawLine(y, color, icon, label, text, true)
	}
	drawStyled := func(y int, color, icon, label, text string) {
		drawLine(y, color, icon, label, text, false)
	}

	if !options.NoInfo {
		draw(3, theme.Accent, "󰎈", "Status", info.Status)
		draw(5, theme.Primary, "󰎆", "Title", info.Title)
		draw(6, theme.SubText, "󰗡", "Artist", info.Artist)
		draw(7, theme.Gray, "󰀥", "Album", info.Album)
		draw(8, theme.Accent, "󰓇", "App", info.Name)
		draw(10, theme.Accent, "󰒝", "Shuffle", info.Shuffle)
		draw(11, theme.Accent, "󰑐", "Loop", info.Loop)

		volume := clampInt(info.Volume, 0, 100)
		const volumeWidth = 12
		volumeProgress := volume * volumeWidth / 100
		volumeBar := theme.Accent + strings.Repeat("=", volumeProgress) + theme.Gray + strings.Repeat("-", volumeWidth-volumeProgress) + theme.Reset
		drawStyled(12, theme.Accent, "󰕾", "Volume", fmt.Sprintf("[%s] %d%%", volumeBar, volume))

		barWidth := cols - offsetX - 18
		if barWidth < 10 {
			barWidth = 10
		}
		progress := 0
		lengthSeconds := info.LengthSeconds
		if lengthSeconds <= 0 {
			lengthSeconds = float64(info.Length)
		}
		if lengthSeconds > 0 {
			progress = int(position / lengthSeconds * float64(barWidth))
		}
		progress = clampInt(progress, 0, barWidth)
		progressBar := theme.Accent + strings.Repeat("=", progress) + theme.Gray + strings.Repeat("-", barWidth-progress) + theme.Reset
		positionSeconds := int(position)
		if positionSeconds < 0 {
			positionSeconds = 0
		}
		timeText := fmt.Sprintf("%02d:%02d / %02d:%02d", positionSeconds/60, positionSeconds%60, info.Length/60, info.Length%60)
		if rows >= 14 {
			fmt.Fprintf(buffer, "\033[14;%dH%s  %s\033[K", offsetX, progressBar, timeText)
		}
	}

	if !options.NoLyrics {
		lyricY := 17
		if options.NoInfo {
			lyricY = 4
		}
		currentIndex, nextIndex := lyricsview.CurrentAndNext(lyricsSnapshot, position)
		maxWidth := lyricTextWidth(cols, offsetX)
		lastLyricRow := rows - 2
		if options.LyricsOnly {
			maxLines := rows - 2
			if maxLines < 1 {
				maxLines = 1
			}
			if len(lyricsSnapshot) > 0 {
				activeIndex := currentIndex
				if activeIndex < 0 {
					activeIndex = nextIndex
				}
				if activeIndex < 0 {
					activeIndex = 0
				}
				start, end := lyricsview.Window(len(lyricsSnapshot), activeIndex, maxLines)
				row := 1
				for index := start; index < end; index++ {
					var wrapped []string
					if index == currentIndex {
						wrapped = lyricsview.RenderKaraokeLineWrapped(lyricsSnapshot[index], position, theme, maxWidth)
						row = writeWrappedLyric(buffer, row, lastLyricRow, offsetX, wrapped, "", "", "")
					} else {
						wrapped = lyricsview.WrapText(lyricsSnapshot[index].Text, maxWidth)
						row = writeWrappedLyric(buffer, row, lastLyricRow, offsetX, wrapped, "", theme.Gray, theme.Reset)
					}
				}
			}
		} else if len(lyricsSnapshot) == 0 {
			// wipeTextArea already cleared the lyric region.
		} else if currentIndex < 0 {
			row := lyricY + 1
			if nextIndex >= 0 && nextIndex < len(lyricsSnapshot) {
				marker := "🎤 "
				markerWidth := textCellWidth(marker)
				wrapped := lyricsview.WrapText(lyricsSnapshot[nextIndex].Text, maxWidth-markerWidth)
				row = writeWrappedLyric(buffer, row, lastLyricRow, offsetX, wrapped, theme.Gray+marker, "", theme.Reset)
			}
		} else {
			row := lyricY
			if currentIndex > 0 {
				wrapped := lyricsview.WrapText(lyricsSnapshot[currentIndex-1].Text, maxWidth)
				row = writeWrappedLyric(buffer, row, lastLyricRow, offsetX, wrapped, "", theme.Gray, theme.Reset)
			} else {
				row++
			}
			marker := "🎤 "
			markerWidth := textCellWidth(marker)
			wrapped := lyricsview.RenderKaraokeLineWrapped(lyricsSnapshot[currentIndex], position, theme, maxWidth-markerWidth)
			row = writeWrappedLyric(buffer, row, lastLyricRow, offsetX, wrapped, theme.Primary+marker, "", "")
			if currentIndex+1 < len(lyricsSnapshot) {
				wrapped = lyricsview.WrapText(lyricsSnapshot[currentIndex+1].Text, maxWidth)
				row = writeWrappedLyric(buffer, row, lastLyricRow, offsetX, wrapped, "", theme.Gray, theme.Reset)
			}
		}
	}

	buffer.WriteString("\033[H")
	return artErr
}

func lyricTextWidth(cols, offsetX int) int {
	// Leave the final terminal column unused. This avoids the terminal's
	// pending-wrap behavior when a lyric row is followed by EL (\033[K).
	width := cols - offsetX
	if width < 1 {
		return 1
	}
	return width
}

func textCellWidth(value string) int {
	width := 0
	for _, character := range value {
		width += runeCellWidth(character)
	}
	return width
}

func writeWrappedLyric(buffer *bytes.Buffer, row, lastRow, column int, chunks []string, firstPrefix, rowPrefix, reset string) int {
	for index, chunk := range chunks {
		if row > lastRow {
			break
		}
		prefix := rowPrefix
		if index == 0 {
			prefix += firstPrefix
		}
		fmt.Fprintf(buffer, "\033[%d;%dH%s%s%s\033[K", row, column, prefix, chunk, reset)
		row++
	}
	return row
}

func textOffset(cols, rows int, options frameOptions) int {
	offsetX := 40
	if rows < 25 {
		offsetX = 32
	}
	if options.NoArt {
		offsetX = 4
	}
	if cols > 0 && offsetX >= cols {
		offsetX = 1
	}
	return offsetX
}

// wipeTextArea clears the text panel before drawing a frame. EL only clears
// the cells from the text column onward, preserving album art on the left;
// clearing every row also removes continuation rows left by terminal wrapping.
func wipeTextArea(buffer *bytes.Buffer, cols, rows int, options frameOptions) {
	if rows < 1 || options.NoInfo && options.NoLyrics {
		return
	}
	offsetX := textOffset(cols, rows, options)
	for row := 1; row <= rows; row++ {
		fmt.Fprintf(buffer, "\033[%d;%dH\033[K", row, offsetX)
	}
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	value = lyricsview.SafeText(value)
	var builder strings.Builder
	used := 0
	for _, character := range value {
		width := runeCellWidth(character)
		if used+width > max {
			break
		}
		builder.WriteRune(character)
		used += width
	}
	return builder.String()
}

func runeCellWidth(character rune) int {
	if character == 0 || unicode.IsControl(character) || unicode.Is(unicode.Mn, character) || unicode.Is(unicode.Me, character) {
		return 0
	}
	if (character >= 0x1100 && character <= 0x115f) ||
		(character >= 0x2329 && character <= 0x232a) ||
		(character >= 0x2e80 && character <= 0xa4cf) ||
		(character >= 0xac00 && character <= 0xd7a3) ||
		(character >= 0xf900 && character <= 0xfaff) ||
		(character >= 0xfe10 && character <= 0xfe6f) ||
		(character >= 0xff00 && character <= 0xff60) ||
		(character >= 0x1f300 && character <= 0x1faff) {
		return 2
	}
	return 1
}

func clampInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
