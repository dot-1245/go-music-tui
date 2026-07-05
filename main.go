package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dolmen-go/kittyimg"
	"github.com/nfnt/resize"
	"golang.org/x/term"
)

type Theme struct {
	Primary, Accent, SubText, Gray, Reset string
}

func hexToANSI(hex string) string {
	hex = strings.TrimSpace(hex)
	if !strings.HasPrefix(hex, "#") || len(hex) != 7 {
		return ""
	}
	var r, g, b uint8
	fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

func loadTheme() Theme {
	home, _ := os.UserHomeDir()
	file, err := os.Open(home + "/matugen-colors.txt")

	t := Theme{
		Primary: "\033[38;2;255;255;255m",
		Accent:  "\033[38;2;238;9;30m",
		SubText: "\033[38;2;255;218;214m",
		Gray:    "\033[38;2;163;139;136m",
		Reset:   "\033[0m",
	}

	if err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}

			key := fields[0]
			val := hexToANSI(fields[1])
			if val == "" {
				continue
			}

			switch key {
			case "primary":
				t.Primary = val
			case "source_color":
				t.Accent = val
			case "on_error_container":
				t.SubText = val
			case "outline":
				t.Gray = val
			}
		}
	}
	return t
}

type PlayerInfo struct {
	Name, Title, Artist, Album, ArtUrl string
	Status, Shuffle, Loop              string
	Volume, Position, Length           int
}

type LyricLine struct {
	Time int
	Text string
}

var (
	currentLyrics        []LyricLine
	lyricMutex           sync.Mutex
	lyricRe              = regexp.MustCompile(`^\[(\d+):(\d+)\.(\d+)\](.*)`)
	currentReqID         int
	currentDisplayArtist string
)

func cmdOut(args ...string) string {
	out, _ := exec.Command("playerctl", args...).Output()
	return strings.TrimSpace(string(out))
}

func cmdRun(args ...string) {
	exec.Command("playerctl", args...).Run()
}

func fetchImage(url string) (image.Image, error) {
	var r io.ReadCloser
	if strings.HasPrefix(url, "http") {
		client := http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return nil, err
		}
		r = resp.Body
	} else {
		f, err := os.Open(strings.TrimPrefix(url, "file://"))
		if err != nil {
			return nil, err
		}
		r = f
	}
	defer r.Close()
	img, _, err := image.Decode(r)
	return img, err
}

func parseLyricsResponse(resp *http.Response, myReqID int) ([]map[string]interface{}, bool) {
	var results []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil || len(results) == 0 {
		return nil, false
	}
	return results, true
}

func fetchLyricsAsync(title, artist, album string, myReqID int) {
	go func() {
		client := http.Client{Timeout: 15 * time.Second}
		var results []map[string]interface{}
		found := false

		apiURL1 := fmt.Sprintf("https://lrclib.net/api/search?track_name=%s&artist_name=%s", url.QueryEscape(title), url.QueryEscape(artist))
		resp1, err := client.Get(apiURL1)
		if err == nil {
			results, found = parseLyricsResponse(resp1, myReqID)
			resp1.Body.Close()
		}

		if !found && album != "" {
			apiURL2 := fmt.Sprintf("https://lrclib.net/api/search?track_name=%s&album_name=%s", url.QueryEscape(title), url.QueryEscape(album))
			resp2, err := client.Get(apiURL2)
			if err == nil {
				results, found = parseLyricsResponse(resp2, myReqID)
				resp2.Body.Close()
			}
		}

		lyricMutex.Lock()
		if myReqID != currentReqID {
			lyricMutex.Unlock()
			return
		}
		lyricMutex.Unlock()

		if !found {
			lyricMutex.Lock()
			if myReqID == currentReqID {
				currentLyrics = []LyricLine{{Time: 0, Text: "No lyrics found :("}}
			}
			lyricMutex.Unlock()
			return
		}

		if officialArtist, ok := results[0]["artistName"].(string); ok && officialArtist != "" {
			lyricMutex.Lock()
			if myReqID == currentReqID {
				currentDisplayArtist = officialArtist
			}
			lyricMutex.Unlock()
		}

		synced, ok := results[0]["syncedLyrics"].(string)
		if !ok || synced == "" {
			lyricMutex.Lock()
			if myReqID == currentReqID {
				currentLyrics = []LyricLine{{Time: 0, Text: "No synced lyrics available"}}
			}
			lyricMutex.Unlock()
			return
		}

		var parsed []LyricLine
		lines := strings.Split(synced, "\n")
		for _, line := range lines {
			matches := lyricRe.FindStringSubmatch(strings.TrimSpace(line))
			if len(matches) >= 5 {
				min, _ := strconv.Atoi(matches[1])
				sec, _ := strconv.Atoi(matches[2])
				text := strings.TrimSpace(matches[4])
				totalSec := min*60 + sec
				parsed = append(parsed, LyricLine{Time: totalSec, Text: text})
			}
		}

		lyricMutex.Lock()
		if myReqID == currentReqID {
			currentLyrics = parsed
		}
		lyricMutex.Unlock()
	}()
}

func main() {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		panic(err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	fmt.Print("\033[?1049h\033[?25l\033[2J")
	defer fmt.Print("\033[?1049l\033[?25h")

	theme := loadTheme()

	home, _ := os.UserHomeDir()
	themePath := home + "/matugen-colors.txt"
	var lastThemeModTime time.Time
	if stat, err := os.Stat(themePath); err == nil {
		lastThemeModTime = stat.ModTime()
	}

	var prevTitle string
	var prevArtist string
	var lastArtUrl string

	inputChan := make(chan byte)
	go func() {
		buf := make([]byte, 1)
		for {
			os.Stdin.Read(buf)
			inputChan <- buf[0]
		}
	}()

	for {
		cols, rows, _ := term.GetSize(int(os.Stdout.Fd()))

		if stat, err := os.Stat(themePath); err == nil {
			if stat.ModTime().After(lastThemeModTime) {
				theme = loadTheme()
				lastThemeModTime = stat.ModTime()
			}
		}

		pList := cmdOut("-l")
		if pList == "" {
			fmt.Print("\033[H\033[K 󰝛 No player found.")
			time.Sleep(1 * time.Second)
			continue
		}
		p := strings.Fields(pList)[0]

		select {
		case key := <-inputChan:
			switch key {
			case ' ':
				cmdRun("-p", p, "play-pause")
			case 'q':
				cmdRun("-p", p, "previous")
			case 'w':
				cmdRun("-p", p, "volume", "0.05+")
			case 'e':
				cmdRun("-p", p, "next")
			case 'a':
				cmdRun("-p", p, "position", "5-")
			case 's':
				cmdRun("-p", p, "volume", "0.05-")
			case 'd':
				cmdRun("-p", p, "position", "5+")
			case 'z':
				cmdRun("-p", p, "shuffle", "Toggle")
			case 'x':
				currentLoop := cmdOut("-p", p, "loop")
				switch currentLoop {
				case "None", "":
					cmdRun("-p", p, "loop", "Track")
				case "Track":
					cmdRun("-p", p, "loop", "Playlist")
				case "Playlist":
					cmdRun("-p", p, "loop", "None")
				default:
					cmdRun("-p", p, "loop", "None")
				}
			case 27, 3:
				return
			}
		default:
		}

		posF, _ := strconv.ParseFloat(cmdOut("-p", p, "position"), 64)
		lenI, _ := strconv.Atoi(cmdOut("-p", p, "metadata", "mpris:length"))
		volF, _ := strconv.ParseFloat(cmdOut("-p", p, "volume"), 64)

		info := PlayerInfo{
			Name: p, Status: cmdOut("-p", p, "status"),
			Title:   cmdOut("-p", p, "metadata", "xesam:title"),
			Artist:  cmdOut("-p", p, "metadata", "xesam:artist"),
			Album:   cmdOut("-p", p, "metadata", "xesam:album"),
			ArtUrl:  cmdOut("-p", p, "metadata", "mpris:artUrl"),
			Shuffle: cmdOut("-p", p, "shuffle"), Loop: cmdOut("-p", p, "loop"),
			Volume: int(volF * 100), Position: int(posF), Length: lenI / 1000000,
		}

		if info.Title != prevTitle || info.Artist != prevArtist {
			theme = loadTheme()

			lyricMutex.Lock()
			currentReqID++
			activeID := currentReqID
			currentLyrics = []LyricLine{{Time: 0, Text: "Loading lyrics..."}}
			currentDisplayArtist = info.Artist
			lyricMutex.Unlock()

			playerNameLower := strings.ToLower(info.Name)
			if strings.Contains(playerNameLower, "spotify") || strings.Contains(playerNameLower, "mpv") {
				fetchLyricsAsync(info.Title, info.Artist, info.Album, activeID)
			} else {
				lyricMutex.Lock()
				currentLyrics = []LyricLine{{Time: 0, Text: "Lyrics not supported for this player"}}
				lyricMutex.Unlock()
			}

			prevTitle = info.Title
			prevArtist = info.Artist
		}

		if info.ArtUrl != lastArtUrl {
			fmt.Print("\x1b_Ga=d\x1b\\")
			if info.ArtUrl != "" {
				if img, err := fetchImage(info.ArtUrl); err == nil {
					imgSize := uint(250)
					if rows < 25 {
						imgSize = 180
					}
					resized := resize.Resize(imgSize, 0, img, resize.Lanczos3)
					fmt.Print("\033[2;2H")
					kittyimg.Fprintln(os.Stdout, resized)
					lastArtUrl = info.ArtUrl
				} else {
					lastArtUrl = ""
				}
			} else {
				lastArtUrl = ""
			}
		}

		lyricMutex.Lock()
		if currentDisplayArtist != "" {
			info.Artist = currentDisplayArtist
		}
		lyricMutex.Unlock()

		offsetX := 40
		if rows < 25 {
			offsetX = 32
		}
		draw := func(y int, color, icon, label, text string) {
			limit := cols - offsetX - 10
			if limit > 0 && len(text) > limit {
				text = text[:limit]
			}
			fmt.Printf("\033[%d;%dH%s%s %s%-8s: %s%s\033[K", y, offsetX, color, icon, theme.Gray, label, theme.Reset, text)
		}

		draw(3, theme.Accent, "󰎈", "Status", info.Status)
		draw(5, theme.Primary, "󰎆", "Title", info.Title)
		draw(6, theme.SubText, "󰗡", "Artist", info.Artist)
		draw(7, theme.Gray, "󰀥", "Album", info.Album)
		draw(8, theme.Accent, "󰓇", "App", info.Name)

		draw(10, theme.Accent, "󰒝", "Shuffle", info.Shuffle)
		draw(11, theme.Accent, "󰑐", "Loop", info.Loop)

		volW := 12
		volP := info.Volume * volW / 100
		if volP > volW { volP = volW }
		if volP < 0 { volP = 0 }
		volBar := theme.Accent + strings.Repeat("=", volP) + theme.Gray + strings.Repeat("-", volW-volP) + theme.Reset
		draw(12, theme.Accent, "󰕾", "Volume", fmt.Sprintf("[%s] %d%%", volBar, info.Volume))

		barW := cols - offsetX - 18
		if barW < 10 { barW = 10 }
		prog := 0
		if info.Length > 0 {
			prog = info.Position * barW / info.Length
		}
		if prog > barW { prog = barW }
		if prog < 0 { prog = 0 }

		barStr := theme.Accent + strings.Repeat("=", prog) + theme.Gray + strings.Repeat("-", barW-prog) + theme.Reset
		timeStr := fmt.Sprintf("%02d:%02d / %02d:%02d", info.Position/60, info.Position%60, info.Length/60, info.Length%60)
		fmt.Printf("\033[14;%dH%s  %s\033[K", offsetX, barStr, timeStr)

		lyricMutex.Lock()
		currentText := "..."
		currentIdx := -1
		nextIdx := -1

		for i, line := range currentLyrics {
			if info.Position >= line.Time {
				currentIdx = i
			} else {
				nextIdx = i
				break
			}
		}

		lyricY := 17

		if currentIdx == -1 {
			fmt.Printf("\033[%d;%dH\033[K", lyricY, offsetX)
			fmt.Printf("\033[%d;%dH%s🎤 %s\033[K", lyricY+1, offsetX, theme.Gray, currentText)
			if nextIdx != -1 && nextIdx < len(currentLyrics) {
				fmt.Printf("\033[%d;%dH%s%s\033[K", lyricY+2, offsetX, theme.Gray, currentLyrics[nextIdx].Text)
			} else {
				fmt.Printf("\033[%d;%dH\033[K", lyricY+2, offsetX)
			}
		} else {
			currentText = currentLyrics[currentIdx].Text

			if currentIdx > 0 {
				fmt.Printf("\033[%d;%dH%s%s\033[K", lyricY, offsetX, theme.Gray, currentLyrics[currentIdx-1].Text)
			} else {
				fmt.Printf("\033[%d;%dH\033[K", lyricY, offsetX)
			}
			fmt.Printf("\033[%d;%dH%s🎤 %s\033[K", lyricY+1, offsetX, theme.Primary, currentText)
			if currentIdx+1 < len(currentLyrics) {
				fmt.Printf("\033[%d;%dH%s%s\033[K", lyricY+2, offsetX, theme.Gray, currentLyrics[currentIdx+1].Text)
			} else {
				fmt.Printf("\033[%d;%dH\033[K", lyricY+2, offsetX)
			}
		}
		lyricMutex.Unlock()

		fmt.Printf("\033[%d;2H%s[w/s] Vol | [q/e] Prev/Next | [a/d] Seek | [z/x] Shuffle/Loop | [Space] Toggle | [ESC] Quit%s", rows-1, theme.Gray, theme.Reset)

		fmt.Print("\033[H")
		time.Sleep(100 * time.Millisecond)
	}
}
