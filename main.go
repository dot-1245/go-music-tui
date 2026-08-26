package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dot-1245/go-music-tui/internal/artwork"
	"github.com/dot-1245/go-music-tui/internal/player"
	"github.com/dot-1245/go-music-tui/internal/ttycolor"
	"golang.org/x/term"
)

var (
	flagNoInfo   = flag.Bool("noinfo", false, "曲情報とプログレスバーを非表示にする")
	flagNoLyrics = flag.Bool("nolyrics", false, "歌詞を非表示にする（取得処理自体も省略）")
	flagNoArt    = flag.Bool("noart", false, "アルバムアートを非表示にする（取得処理自体も省略）")
	flagColor    = flag.String("color", string(ttycolor.ModeAuto), "色の出力: auto, always, or never")
	flagPlayer   = flag.String("player", "", "監視するプレイヤー名（省略時は最も最近更新されたプレイヤー）")
	// --debug: 歌詞取得の各段階(検索クエリ・HTTPエラー・類似度フィルタでの
	// 却下理由・最終的にどの候補を選んだか等)を ~/.cache/go-music-tui-debug.log
	// に書き出す。TUI自体は画面を占有しているのでstdout/stderrには出さず、
	// 別途 `tail -f ~/.cache/go-music-tui-debug.log` で追いかける想定。
	flagDebug = flag.Bool("debug", false, "歌詞取得の詳細ログを ~/.cache/go-music-tui-debug.log に出力する")
)

const (
	frameInterval    = 50 * time.Millisecond
	playerctlTimeout = 800 * time.Millisecond
)

var (
	debugLogFile *os.File
	debugMutex   sync.Mutex
)

// initDebugLog は --debug 指定時にログファイルを開く。失敗した場合は
// デバッグログ無効のまま続行する(ログが取れないだけで本体機能には影響しない)。
func initDebugLog() {
	if !*flagDebug {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	dir := filepath.Join(home, ".cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "go-music-tui-debug.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	debugLogFile = f
}

// debugf は --debug 指定時のみログファイルに1行書き込む。
func debugf(format string, args ...interface{}) {
	if !*flagDebug || debugLogFile == nil {
		return
	}
	debugMutex.Lock()
	defer debugMutex.Unlock()
	_, _ = fmt.Fprintf(debugLogFile, "[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

type controlRequest struct {
	player string
	args   []string
}

func main() {
	flag.Parse()
	if *flagNoInfo && *flagNoLyrics && *flagNoArt {
		fmt.Fprintln(os.Stderr, "--noinfo --nolyrics --noart を同時に指定することはできません")
		os.Exit(1)
	}
	colorMode, err := ttycolor.ParseMode(*flagColor)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	initDebugLog()
	if debugLogFile != nil {
		defer debugLogFile.Close()
	}
	debugf("==== go-music-tui started (noinfo=%v nolyrics=%v noart=%v color=%s player=%q) ====", *flagNoInfo, *flagNoLyrics, *flagNoArt, colorMode, *flagPlayer)

	isArtOnly := *flagNoInfo && *flagNoLyrics && !*flagNoArt
	isLyricsOnly := *flagNoInfo && *flagNoArt && !*flagNoLyrics
	stdinFD := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(stdinFD)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot enter raw terminal mode: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = term.Restore(stdinFD, oldState) }()

	_, _ = fmt.Fprint(os.Stdout, "\033[?1049h\033[?25l\033[2J")
	defer func() { _, _ = fmt.Fprint(os.Stdout, "\033[?1049l\033[?25h") }()

	baseContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, stopSignals := signal.NotifyContext(baseContext, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	theme := ttycolor.New(colorMode, os.Stdout)
	playerClient := player.NewWithTimeout(nil, playerctlTimeout)
	lyricsClient := newRuntimeLyricClient()
	lyricsState := newLyricsState()
	artworkState := newArtworkState(artwork.NewFetcher(nil), debugf)
	defer lyricsState.Stop()
	defer artworkState.Close()

	playerEvents := playerClient.Follow(ctx, *flagPlayer)
	inputChan := make(chan byte, 32)
	go readInput(ctx, os.Stdin, inputChan)
	controlChan := make(chan controlRequest, 32)
	go runControlWorker(ctx, playerClient, controlChan)

	var current player.Snapshot
	hasPlayer := false
	var lyricKey string
	var artworkSource string
	var lastFollowError time.Time
	var renderedArtworkVersion uint64 = ^uint64(0)
	previousArtCols, previousArtRows := -1, -1
	previousHasPlayer := false
	forceClear := true
	var lastFrame []byte

	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-playerEvents:
			if !ok {
				return
			}
			if event.Err != nil {
				if lastFollowError.IsZero() || time.Since(lastFollowError) >= 2*time.Second {
					debugf("playerctl follow: %v", event.Err)
					lastFollowError = time.Now()
				}
			}
			if event.Stopped {
				if hasPlayer {
					hasPlayer = false
					forceClear = true
				}
				lyricsState.Stop()
				artworkState.Request(ctx, "")
				lyricKey = ""
				artworkSource = ""
			}
			if event.Snapshot != nil {
				current = *event.Snapshot
				if !hasPlayer {
					forceClear = true
				}
				hasPlayer = true
				info := current.Info
				newLyricKey := lyricTrackKey(info)
				if newLyricKey != lyricKey {
					lyricKey = newLyricKey
					artists := append([]string(nil), info.Artists...)
					if len(artists) == 0 {
						artists = player.SplitArtistsFallback(info.Artist)
					}
					artists = player.FlattenArtists(artists)
					if *flagNoLyrics {
						lyricsState.Stop()
					} else {
						lyricsState.Start(ctx, lyricsClient, info.Title, artists, info.Artist, info.Album, info.Length)
					}
				}
				if !*flagNoArt && info.ArtUrl != artworkSource {
					artworkSource = info.ArtUrl
					artworkState.Request(ctx, info.ArtUrl)
				}
			}

		case key, ok := <-inputChan:
			if !ok {
				return
			}
			if key == 27 || key == 3 {
				return
			}
			if !hasPlayer {
				continue
			}
			if args, ok := controlArgs(key, current.Info); ok {
				enqueueControl(controlChan, controlRequest{player: current.Info.Name, args: args})
			}

		case <-ticker.C:
			cols, rows, sizeErr := term.GetSize(int(os.Stdout.Fd()))
			if sizeErr != nil || cols < 1 || rows < 1 {
				debugf("terminal size unavailable: %v (%dx%d)", sizeErr, cols, rows)
				continue
			}
			if cols != previousArtCols || rows != previousArtRows {
				forceClear = true
				previousArtCols, previousArtRows = cols, rows
			}
			if hasPlayer != previousHasPlayer {
				forceClear = true
				previousHasPlayer = hasPlayer
			}

			artSnapshot := artworkState.Snapshot()
			redrawArtwork := !*flagNoArt && (forceClear || artSnapshot.Version != renderedArtworkVersion)
			lyricsSnapshot, _ := lyricsState.Snapshot()
			var frame bytes.Buffer
			if forceClear {
				frame.WriteString("\033[2J")
			}
			frameErr := buildFrame(&frame, current, hasPlayer, time.Now(), cols, rows, os.Stdout, theme, lyricsSnapshot, artSnapshot, redrawArtwork, frameOptions{
				NoInfo: *flagNoInfo, NoLyrics: *flagNoLyrics, NoArt: *flagNoArt,
				ArtOnly: isArtOnly, LyricsOnly: isLyricsOnly,
			})
			if !bytes.Equal(frame.Bytes(), lastFrame) {
				written, writeErr := os.Stdout.Write(frame.Bytes())
				if writeErr == nil && written != frame.Len() {
					writeErr = io.ErrShortWrite
				}
				if writeErr != nil {
					debugf("frame write failed: %v", writeErr)
					return
				}
				lastFrame = append(lastFrame[:0], frame.Bytes()...)
			}
			if frameErr != nil {
				debugf("frame artwork failed: %v", frameErr)
			}
			if redrawArtwork {
				renderedArtworkVersion = artSnapshot.Version
			}
			forceClear = false
		}
	}
}

func readInput(ctx context.Context, reader io.Reader, output chan<- byte) {
	defer close(output)
	buffer := make([]byte, 1)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			select {
			case output <- buffer[0]:
			case <-ctx.Done():
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func controlArgs(key byte, info player.Info) ([]string, bool) {
	action := ""
	switch key {
	case ' ':
		action = "play-pause"
	case 'q':
		action = "previous"
	case 'w':
		action = "volume-up"
	case 'e':
		action = "next"
	case 'a':
		action = "seek-back"
	case 's':
		action = "volume-down"
	case 'd':
		action = "seek-forward"
	case 'z':
		action = "shuffle"
	case 'x':
		action = "loop"
	default:
		return nil, false
	}
	args, ok := player.ActionArgs(action)
	if !ok {
		return nil, false
	}
	if action == "loop" {
		return []string{"loop", player.NextLoop(info.Loop)}, true
	}
	return args, true
}

func enqueueControl(output chan<- controlRequest, request controlRequest) {
	select {
	case output <- request:
	default:
		// A held key must not make the renderer wait. Dropping a control is
		// preferable to growing unbounded input latency.
		debugf("control queue full; dropped %v for player %q", request.args, request.player)
	}
}

func runControlWorker(ctx context.Context, client *player.Client, requests <-chan controlRequest) {
	for {
		select {
		case <-ctx.Done():
			return
		case request, ok := <-requests:
			if !ok {
				return
			}
			if err := client.Control(ctx, request.player, request.args...); err != nil {
				debugf("control failed for %q (%v): %v", request.player, request.args, err)
			}
		}
	}
}

func lyricTrackKey(info player.Info) string {
	return strings.Join([]string{info.Title, info.Artist, info.Album, fmt.Sprint(info.Length)}, "\x00")
}
