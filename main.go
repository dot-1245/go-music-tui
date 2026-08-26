package main

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/dolmen-go/kittyimg"
	"github.com/nfnt/resize"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// --- 表示モードフラグ ---
//
// --noinfo   : 曲情報(Status/Title/Artist/Album/App/Shuffle/Loop/Volume/progress bar)を非表示
// --nolyrics : 歌詞を非表示。描画をスキップするだけでなく、lrclib/MusicBrainzへの
//
//	問い合わせ自体を丸ごとスキップするので、歌詞不要時は動作が軽くなる
//
// --noart    : アルバムアート(kitty画像プロトコル)を非表示。画像取得(HTTP/ファイル読込)も省略
//
// 例:
//
//	--noinfo --nolyrics  → アルバムアートのみ
//	--noinfo --noart     → 歌詞のみ
var (
	flagNoInfo   = flag.Bool("noinfo", false, "曲情報とプログレスバーを非表示にする")
	flagNoLyrics = flag.Bool("nolyrics", false, "歌詞を非表示にする（取得処理自体も省略）")
	flagNoArt    = flag.Bool("noart", false, "アルバムアートを非表示にする（取得処理自体も省略）")
	// --debug: 歌詞取得の各段階(検索クエリ・HTTPエラー・類似度フィルタでの
	// 却下理由・最終的にどの候補を選んだか等)を ~/.cache/go-music-tui-debug.log
	// に書き出す。TUI自体は画面を占有しているのでstdout/stderrには出さず、
	// 別途 `tail -f ~/.cache/go-music-tui-debug.log` で追いかける想定。
	flagDebug = flag.Bool("debug", false, "歌詞取得の詳細ログを ~/.cache/go-music-tui-debug.log に出力する")
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
	home, _ := os.UserHomeDir()
	dir := home + "/.cache"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "go-music-tui-debug.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	debugLogFile = f
}

// debugf は --debug 指定時のみログファイルに1行書き込む。非指定時は
// ほぼノーコスト(bool判定のみ)なので、通常経路にそのまま埋め込んでよい。
func debugf(format string, args ...interface{}) {
	if !*flagDebug || debugLogFile == nil {
		return
	}
	debugMutex.Lock()
	defer debugMutex.Unlock()
	fmt.Fprintf(debugLogFile, "[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

type Theme struct {
	Primary, Accent, SubText, Gray, Reset string
}

func hexToANSI(hex string) string {
	hex = strings.TrimSpace(hex)
	if !strings.HasPrefix(hex, "#") || len(hex) != 7 {
		return ""
	}
	var r, g, b uint8
	n, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	if err != nil || n != 3 {
		return ""
	}
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
			// 以前は "source_color" をAccentに使っていたが、無彩色(モノクロ)に
			// 近いアルバムアートの場合、Material Color Utilitiesのスコアリングが
			// 有効な色を見つけられずGoogleブルー寄りの色(#4285f4等)にフォール
			// バックしてしまい、他が完全にグレースケールでもAccentだけ青くなる
			// 不具合があった。"secondary"は同じ無彩色画像でもちゃんとグレーの
			// トーンになる(実測: #c6c6c6等)ため、こちらを使う。
			case "secondary":
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

// winsize は ioctl(TIOCGWINSZ) が返す構造体。x/term.GetSize は
// 文字セル数(Row/Col)しか返さないが、この構造体自体にはピクセルサイズ
// (Xpixel/Ypixel)も含まれている。--noart時のような「画像を端末いっぱいに
// 広げたい」ケースで、実際の画像ピクセルサイズをどこまで大きくできるか
// 判断するために使う。
type winsize struct {
	Row, Col       uint16
	Xpixel, Ypixel uint16
}

// getTermPixelSize は標準出力先の端末の描画領域サイズをピクセル単位で返す。
// 端末がピクセルサイズを報告しない(0を返す)場合は ok=false。
func getTermPixelSize() (xpixel, ypixel int, ok bool) {
	ws := &winsize{}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stdout),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws)),
	)
	if errno != 0 || ws.Xpixel == 0 || ws.Ypixel == 0 {
		return 0, 0, false
	}
	return int(ws.Xpixel), int(ws.Ypixel), true
}

type PlayerInfo struct {
	Name, Title, Artist, Album, ArtUrl string
	Status, Shuffle, Loop              string
	Volume, Position, Length           int
}

type LyricWord struct {
	Time    float64
	EndTime float64
	Text    string
}

type LyricLine struct {
	Time    float64
	EndTime float64
	Text    string
	Words   []LyricWord
}

// lyricResult is the common representation used by lrclib, SyncLRC, and
// AMLL. Quality is only used to break close metadata matches; word-synced
// results are intentionally ranked above ordinary line-synced lyrics.
type lyricResult struct {
	Title    string
	Artist   string
	Album    string
	Duration float64
	Lines    []LyricLine
	Synced   string
	Plain    string
	Source   string
	Quality  int
}

var (
	currentLyrics        []LyricLine
	lyricMutex           sync.Mutex
	lyricLineTagRe       = regexp.MustCompile(`\[(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?\]`)
	lyricWordTagRe       = regexp.MustCompile(`<(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?>`)
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

func lrclibHTTPGet(client *http.Client, requestURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", providerUserAgent)
	req.Header.Set("Lrclib-Client", "go-music-tui/1.0")
	return client.Do(req)
}

// getArtistList は xesam:artist を --format で1本の文字列に潰さず、
// 生の複数行のまま取得する。
//
// これまでのバグの根本原因: --format "{{xesam:artist}}" を使うと、
// コラボ曲のように xesam:artist が複数値(例: "Imagine Dragons","Ado")
// の場合、playerctlが独自のセパレータ(例: "; ")で1本の文字列に
// 結合してしまう。結果として "Imagine Dragons; Ado" のような、
// lrclibにもMusicBrainzにも実在しないアーティスト名ができあがり、
// 厳密一致・検索・MusicBrainz解決・アーティスト類似度フィルタの
// 全段階が機能しなくなっていた。
//
// `playerctl metadata xesam:artist` (--formatなし)で問い合わせると
// 複数値は1行ずつ出力されるので、ここでそれぞれ個別の要素として取る。
func getArtistList(p string) []string {
	out := cmdOut("-p", p, "metadata", "xesam:artist")
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	var artists []string
	seen := map[string]bool{}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		artists = append(artists, l)
	}
	return artists
}

// splitArtistsFallback は getArtistList が使えない場合(プレイヤーが
// 個別プロパティ問い合わせに対応していない等)向けの保険。
// 既に "; " 等で結合された文字列から、それらしいアーティスト名を
// 分割して救い出す。
var artistSplitRe = regexp.MustCompile(`\s*[;,/]\s*|\s+(?:feat\.?|ft\.?|with|&)\s+`)

func splitArtistsFallback(joined string) []string {
	if joined == "" {
		return nil
	}
	parts := artistSplitRe.Split(joined, -1)
	var artists []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			artists = append(artists, p)
		}
	}
	return artists
}

// flattenArtists は getArtistList が返した各要素をさらに splitArtistsFallback
// で割り、重複を除いて返す。
//
// getArtistList は xesam:artist を playerctl の配列プロパティとして個別に
// 取得しているにもかかわらず、プレイヤー側(特定のSpotifyクライアント連携等)が
// そもそもMPRIS上で xesam:artist を ["Imagine Dragons, Ado"] のように
// "1要素にカンマ結合した状態" で公開しているケースが確認されている。
// この場合 getArtistList は(正しく動作した上で)結合済みの1要素をそのまま
// 返してしまい、後段の lrclib への問い合わせが
// artist_name="Imagine Dragons, Ado" という実在しない名前で飛んでしまう。
// これが原因で、たまたま別の(無関係な)エントリに一致してしまい、
// 本来と異なる歌詞が表示される不具合につながっていた。
// ここで各要素にもう一段 splitArtistsFallback をかけることで、
// getArtistList側が正しく分割できていてもできていなくても、
// 最終的には個別のアーティスト名の集合になるようにする。
func flattenArtists(artists []string) []string {
	var flattened []string
	seen := map[string]bool{}
	for _, a := range artists {
		parts := splitArtistsFallback(a)
		if len(parts) == 0 {
			parts = []string{a}
		}
		for _, p := range parts {
			if p != "" && !seen[p] {
				seen[p] = true
				flattened = append(flattened, p)
			}
		}
	}
	return flattened
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

// featRe は "(feat. XXX)" "[ft. XXX]" "(with XXX)" のようなコラボ注釈を検出する。
// Spotify等はコラボ曲のタイトルにこれを自動付与するが、lrclibの登録タイトルは
// 付いていないことが多く、そのまま検索するとヒットしなくなるため除去する。
var featRe = regexp.MustCompile(`(?i)[\(\[](feat\.?|ft\.?|with)\s+[^\)\]]*[\)\]]`)

// cleanTrackTitle は検索用にタイトルからコラボ注釈を取り除く。
func cleanTrackTitle(title string) string {
	cleaned := featRe.ReplaceAllString(title, "")
	return strings.TrimSpace(cleaned)
}

// instRe は "(Instrumental)" "(Inst.)" "(off vocal)" "(カラオケ)" のような
// インスト版を示す注釈を検出する。この手の曲は元々歌詞が存在しないため、
// タイトルだけが似ている無関係な曲を誤って引っ張ってくる原因になりやすい。
var instRe = regexp.MustCompile(`(?i)[\(\[【](inst(?:rumental)?\.?|off\s*vocal|karaoke|カラオケ|インスト(?:ゥルメンタル)?)[\)\]】]`)

// isInstrumentalTitle はタイトルにインスト版を示す注釈が含まれているかを判定する。
func isInstrumentalTitle(title string) bool {
	return instRe.MatchString(title)
}

// searchLyrics は lrclib の検索APIを叩いて結果を返す。失敗時は空スライス。
func searchLyrics(client *http.Client, params url.Values) []map[string]interface{} {
	resp, err := lrclibHTTPGet(client, "https://lrclib.net/api/search?"+params.Encode())
	if err != nil {
		debugf("  search HTTP error (%s): %v", params.Encode(), err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		debugf("  search unexpected status %d (%s)", resp.StatusCode, params.Encode())
	}
	var results []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		debugf("  search decode error (%s): %v", params.Encode(), err)
		return nil
	}
	return results
}

// levenshtein は2つの文字列間の編集距離をrune単位で計算する。
func levenshtein(a, b []rune) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// titleSimilarity は2つの曲名を大文字小文字・前後空白を無視して比較し、
// 0.0(まったく違う)〜1.0(完全一致)の類似度を返す。
func titleSimilarity(a, b string) float64 {
	ra := []rune(strings.ToLower(strings.TrimSpace(a)))
	rb := []rune(strings.ToLower(strings.TrimSpace(b)))
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	if maxLen == 0 {
		return 1
	}
	dist := levenshtein(ra, rb)
	return 1 - float64(dist)/float64(maxLen)
}

// minTitleSimilarity を下回る候補は「たまたま再生時間が近いだけの無関係な曲」
// とみなして除外する。低すぎるとインスト曲などで誤検出したまま拾ってしまい、
// 高すぎると表記ゆれ吸収の効果が薄れるので、様子を見て調整してよい値。
const minTitleSimilarity = 0.4

// normalizeArtistName は "姓, 名" と "名 姓" のような語順違いを吸収するため、
// カンマ・セミコロン・アンパサンド・スラッシュを空白に置き換えてトークンに
// 分割し、アルファベット順に並べ替えてから結合する。例: "Nakamori, Akina" と
// "Akina Nakamori" は両方とも "akina nakamori" に正規化されて一致するように
// なる。
//
// もともとカンマしか置換していなかったため、"Imagine Dragons; Ado" のような
// セミコロン結合の複数アーティスト文字列が "dragons;" のようなゴミトークンの
// まま比較されてしまい、正しい候補まで類似度フィルタで弾かれるバグがあった。
// (ただし漢字表記とローマ字表記のような、文字種そのものが違う場合は
// この正規化では救えない。MusicBrainzのエイリアス解決側で吸収する想定。)
func normalizeArtistName(s string) string {
	replacer := strings.NewReplacer(",", " ", ";", " ", "&", " ", "/", " ")
	s = replacer.Replace(strings.ToLower(s))
	tokens := strings.Fields(s)
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

// bestSimilarityAgainst は candidate と targets の中で最も高い類似度を返す。
// targetsが空の場合は「比較対象なし」として1.0(制約なし)を返す。
func bestSimilarityAgainst(candidate string, targets []string) float64 {
	if len(targets) == 0 {
		return 1
	}
	best := 0.0
	candidateVariants := append([]string{candidate}, splitArtistsFallback(candidate)...)
	for _, candidateVariant := range candidateVariants {
		if normalizeArtistName(candidateVariant) == "" {
			continue
		}
		for _, t := range targets {
			targetVariants := append([]string{t}, splitArtistsFallback(t)...)
			for _, targetVariant := range targetVariants {
				sim := titleSimilarity(normalizeArtistName(candidateVariant), normalizeArtistName(targetVariant))
				if sim > best {
					best = sim
				}
			}
		}
	}
	return best
}

// minArtistSimilarity を下回る候補は「タイトルは似ているが実際は無関係な
// アーティストの曲」とみなして除外する。タイトルがありふれた単語だと
// (例: "不思議")、別のアーティストの同名曲を拾ってしまうことがあるため、
// タイトル類似度だけでなくアーティスト類似度でも絞り込む。
const minArtistSimilarity = 0.4

// pickBestMatch は同期歌詞を持ち、かつ検索対象の曲名・アーティスト名と
// ある程度似ている候補の中から、目標の再生時間に一番近いものを選ぶ。
// targetArtistsには「元のアーティスト名(複数可)」に加えてMusicBrainzで
// 解決した別名義(エイリアス)も渡すことで、表記ゆれは許容しつつ、
// タイトルが偶然同じなだけの完全に無関係なアーティストの曲は弾く。
func pickBestMatch(results []map[string]interface{}, targetDuration int, targetTitle string, targetArtists []string) map[string]interface{} {
	var best map[string]interface{}
	bestDiff := math.MaxFloat64
	for _, r := range results {
		synced := mapStringValue(r, "syncedLyrics", "synced")
		lyricsfile := mapStringValue(r, "lyricsfile")
		if synced == "" && (lyricsfile == "" || func() bool {
			lines, err := parseLyricsfile(lyricsfile)
			return err != nil || len(lines) == 0
		}()) {
			continue
		}

		trackName, _ := r["trackName"].(string)
		artistName, _ := r["artistName"].(string)

		if trackName != "" {
			if sim := titleSimilarity(trackName, targetTitle); sim < minTitleSimilarity {
				debugf("  reject %q / %q: title similarity %.2f < %.2f (target title=%q)", trackName, artistName, sim, minTitleSimilarity, targetTitle)
				continue
			}
		}

		if artistName != "" {
			if sim := bestSimilarityAgainst(artistName, targetArtists); sim < minArtistSimilarity {
				debugf("  reject %q / %q: artist similarity %.2f < %.2f (target artists=%v)", trackName, artistName, sim, minArtistSimilarity, targetArtists)
				continue
			}
		}

		dur, _ := r["duration"].(float64)
		diff := math.Abs(dur - float64(targetDuration))
		debugf("  candidate %q / %q: durationDiff=%.1fs (theirs=%.0fs, target=%ds)", trackName, artistName, diff, dur, targetDuration)
		if diff < bestDiff {
			bestDiff = diff
			best = r
		}
	}
	return best
}

// --- MusicBrainz連携 ---
//
// lrclibのテキスト検索だけだと、Spotifyの日本語ローカライズ表記(例:
// "Radiohead"が現地語表記に化ける等)や、アーティストの別名義(例:
// かめりあ / Camellia / Cametek)を吸収できない。
// MusicBrainzは「同一人物の別名義」をエイリアスとして正式に持っているので、
// 一度MusicBrainzで曲を正規化し、そのアーティストの別名義を全部取得してから
// それぞれの名義でlrclibを検索することで、表記ゆれをかなり吸収できる。
//
// 注意: MusicBrainz APIは「意味のあるUser-Agent(アプリ名/バージョン/連絡先)」を
// 要求しており、汎用的すぎる/未設定のUAはブロック・格下げされる。
// mbUserAgentは自分のリポジトリURLや連絡先に書き換えて使うこと。
const mbUserAgent = "go-music-tui/1.0 (https://github.com/dot-1245/go-music-tui)"

type mbAlias struct {
	Name string `json:"name"`
}

type mbArtistDetail struct {
	Name    string    `json:"name"`
	Aliases []mbAlias `json:"aliases"`
}

type mbArtistCredit struct {
	Artist struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}

type mbRecording struct {
	Title        string           `json:"title"`
	ArtistCredit []mbArtistCredit `json:"artist-credit"`
}

type mbSearchResp struct {
	Recordings []mbRecording `json:"recordings"`
}

// mbQueryEscape は MusicBrainz の検索クエリ(Lucene構文)で
// 特別な意味を持つ最低限の文字をエスケープする。
func mbQueryEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
	)
	return r.Replace(s)
}

func mbGet(client *http.Client, path string) ([]byte, error) {
	req, err := http.NewRequest("GET", "https://musicbrainz.org/ws/2/"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", mbUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// mbResolve は MusicBrainz でタイトル+アーティストから録音を検索し、
// 正規化されたタイトルと、そのアーティストの別名義(エイリアス)を集めて返す。
// 例: title="ノーウェアエレベータ", artist="かめりあ"
//
//	-> canonicalTitle="Nowhere Elevator" (登録されていれば), artistNames=["かめりあ","Camellia","Cametek",...]
//
// 見つからない場合は ok=false。
func mbResolve(client *http.Client, title, artist string) (canonicalTitle string, artistNames []string, ok bool) {
	q := fmt.Sprintf(`recording:"%s" AND artist:"%s"`, mbQueryEscape(title), mbQueryEscape(artist))
	body, err := mbGet(client, "recording?query="+url.QueryEscape(q)+"&fmt=json&limit=5")
	if err != nil {
		debugf("  musicbrainz HTTP error (query=%q): %v", q, err)
		return "", nil, false
	}
	var result mbSearchResp
	if err := json.Unmarshal(body, &result); err != nil {
		debugf("  musicbrainz decode error (query=%q): %v", q, err)
		return "", nil, false
	}
	if len(result.Recordings) == 0 {
		debugf("  musicbrainz: no recordings for query=%q", q)
		return "", nil, false
	}

	rec := result.Recordings[0]
	if rec.Title == "" || len(rec.ArtistCredit) == 0 {
		debugf("  musicbrainz: first recording missing title/artist-credit (query=%q)", q)
		return "", nil, false
	}

	primary := rec.ArtistCredit[0].Artist
	artistNames = append(artistNames, primary.Name)

	if primary.ID != "" {
		// MusicBrainzは未認証だと1req/秒程度のゆるいレート制限があるため、
		// 連続で叩く前に一呼吸置く(行儀よく使うため)。
		time.Sleep(1100 * time.Millisecond)

		if aliasBody, err := mbGet(client, "artist/"+primary.ID+"?inc=aliases&fmt=json"); err == nil {
			var detail mbArtistDetail
			if json.Unmarshal(aliasBody, &detail) == nil {
				seen := map[string]bool{primary.Name: true}
				for _, al := range detail.Aliases {
					if al.Name == "" || seen[al.Name] {
						continue
					}
					seen[al.Name] = true
					artistNames = append(artistNames, al.Name)
					if len(artistNames) >= 6 { // 際限なく増やさないための上限
						break
					}
				}
			}
		}
	}

	return rec.Title, artistNames, true
}

func parseLrcTimestamp(minutes, seconds, fraction string) (float64, bool) {
	minuteValue, err := strconv.Atoi(minutes)
	if err != nil {
		return 0, false
	}
	secondValue, err := strconv.Atoi(seconds)
	if err != nil {
		return 0, false
	}

	fractionValue := 0.0
	if fraction != "" {
		fractionValue, err = strconv.ParseFloat("0."+fraction, 64)
		if err != nil {
			return 0, false
		}
	}

	total := float64(minuteValue*60+secondValue) + fractionValue
	return total, !math.IsNaN(total) && !math.IsInf(total, 0)
}

func lrcTimestampFromMatch(source string, match []int) (float64, bool) {
	if len(match) < 6 {
		return 0, false
	}
	value := func(group int) string {
		start, end := match[group*2], match[group*2+1]
		if start < 0 || end < 0 {
			return ""
		}
		return source[start:end]
	}
	return parseLrcTimestamp(value(1), value(2), value(3))
}

// parseInlineWordTags parses enhanced-LRC word timestamps such as
// "<00:12.34>Hello <00:12.80>world". The line-level timestamps are removed
// before scanning the inline tags so both forms can coexist in one row.
func parseInlineWordTags(row string) (string, []LyricWord) {
	withoutLineTags := lyricLineTagRe.ReplaceAllString(row, "")
	matches := lyricWordTagRe.FindAllStringSubmatchIndex(withoutLineTags, -1)
	words := make([]LyricWord, 0, len(matches))
	cursor := 0
	currentTime := -1.0

	for _, match := range matches {
		time, ok := lrcTimestampFromMatch(withoutLineTags, match)
		if !ok {
			continue
		}

		if currentTime >= 0 {
			text := withoutLineTags[cursor:match[0]]
			if strings.TrimSpace(text) != "" {
				words = append(words, LyricWord{
					Time:    currentTime,
					EndTime: time,
					Text:    text,
				})
			}
		}

		currentTime = time
		cursor = match[1]
	}

	if currentTime >= 0 {
		text := withoutLineTags[cursor:]
		if strings.TrimSpace(text) != "" {
			words = append(words, LyricWord{
				Time:    currentTime,
				EndTime: math.NaN(),
				Text:    text,
			})
		}
	}

	if len(words) == 0 {
		return strings.TrimSpace(withoutLineTags), nil
	}

	var textBuilder strings.Builder
	for _, word := range words {
		textBuilder.WriteString(word.Text)
	}
	return strings.TrimSpace(textBuilder.String()), words
}

func normalizeLyricLines(lines []LyricLine) []LyricLine {
	sort.SliceStable(lines, func(i, j int) bool {
		return lines[i].Time < lines[j].Time
	})

	for i := range lines {
		if math.IsNaN(lines[i].EndTime) || math.IsInf(lines[i].EndTime, 0) || lines[i].EndTime <= lines[i].Time {
			if i+1 < len(lines) {
				lines[i].EndTime = lines[i+1].Time
			}
		}

		sort.SliceStable(lines[i].Words, func(left, right int) bool {
			return lines[i].Words[left].Time < lines[i].Words[right].Time
		})
		for j := range lines[i].Words {
			word := &lines[i].Words[j]
			if math.IsNaN(word.EndTime) || math.IsInf(word.EndTime, 0) || word.EndTime <= word.Time {
				if j+1 < len(lines[i].Words) {
					word.EndTime = lines[i].Words[j+1].Time
				} else if !math.IsNaN(lines[i].EndTime) && !math.IsInf(lines[i].EndTime, 0) && lines[i].EndTime > word.Time {
					word.EndTime = lines[i].EndTime
				}
			}
		}
	}
	return lines
}

// parseSyncedLyrics accepts ordinary LRC and enhanced LRC. An enhanced row
// keeps both the complete line text and its word-level timing information.
func parseSyncedLyrics(synced string) []LyricLine {
	parsed := make([]LyricLine, 0)
	for _, row := range strings.Split(strings.ReplaceAll(synced, "\r\n", "\n"), "\n") {
		text, words := parseInlineWordTags(row)
		matches := lyricLineTagRe.FindAllStringSubmatchIndex(row, -1)
		if len(matches) == 0 && len(words) > 0 {
			parsed = append(parsed, LyricLine{
				Time:    words[0].Time,
				EndTime: math.NaN(),
				Text:    text,
				Words:   words,
			})
			continue
		}
		if len(matches) == 0 || (text == "" && len(words) == 0) {
			continue
		}

		for _, match := range matches {
			time, ok := lrcTimestampFromMatch(row, match)
			if !ok {
				continue
			}
			lineWords := append([]LyricWord(nil), words...)
			parsed = append(parsed, LyricLine{
				Time:    time,
				EndTime: math.NaN(),
				Text:    text,
				Words:   lineWords,
			})
		}
	}
	return normalizeLyricLines(parsed)
}

type ttmlNode struct {
	Name     string
	Attrs    map[string]string
	Children []ttmlChild
}

type ttmlChild struct {
	Node *ttmlNode
	Text string
}

func parseTTMLDocument(source string) (*ttmlNode, error) {
	decoder := xml.NewDecoder(strings.NewReader(source))
	var root *ttmlNode
	stack := make([]*ttmlNode, 0)

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch value := token.(type) {
		case xml.StartElement:
			node := &ttmlNode{
				Name:  value.Name.Local,
				Attrs: make(map[string]string),
			}
			for _, attr := range value.Attr {
				node.Attrs[attr.Name.Local] = attr.Value
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("multiple TTML roots")
				}
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, ttmlChild{Node: node})
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("unexpected TTML closing tag")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 && len(value) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children,
					ttmlChild{Text: string(value)})
			}
		}
	}

	if root == nil || len(stack) != 0 {
		return nil, fmt.Errorf("invalid TTML document")
	}
	return root, nil
}

func ttmlAttribute(node *ttmlNode, name string) string {
	if node == nil {
		return ""
	}
	return node.Attrs[name]
}

func parseTTMLTime(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	parts := strings.Split(value, ":")
	if len(parts) == 2 || len(parts) == 3 {
		seconds, err := strconv.ParseFloat(parts[len(parts)-1], 64)
		if err != nil {
			return 0, false
		}
		minutes, err := strconv.ParseFloat(parts[len(parts)-2], 64)
		if err != nil {
			return 0, false
		}
		hours := 0.0
		if len(parts) == 3 {
			hours, err = strconv.ParseFloat(parts[0], 64)
			if err != nil {
				return 0, false
			}
		}
		total := hours*3600 + minutes*60 + seconds
		return total, !math.IsNaN(total) && !math.IsInf(total, 0)
	}

	lower := strings.ToLower(value)
	multiplier := 1.0
	number := lower
	switch {
	case strings.HasSuffix(lower, "ms"):
		number = strings.TrimSuffix(lower, "ms")
		multiplier = 0.001
	case strings.HasSuffix(lower, "h"):
		number = strings.TrimSuffix(lower, "h")
		multiplier = 3600
	case strings.HasSuffix(lower, "m"):
		number = strings.TrimSuffix(lower, "m")
		multiplier = 60
	case strings.HasSuffix(lower, "s"):
		number = strings.TrimSuffix(lower, "s")
		multiplier = 1
	default:
		multiplier = 1
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
	if err != nil {
		return 0, false
	}
	total := parsed * multiplier
	return total, !math.IsNaN(total) && !math.IsInf(total, 0)
}

func normalizeTTMLText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func ttmlRole(node *ttmlNode) string {
	return ttmlAttribute(node, "role")
}

func ttmlMainText(node *ttmlNode) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	for _, child := range node.Children {
		if child.Node == nil {
			builder.WriteString(normalizeTTMLText(child.Text))
			continue
		}
		role := ttmlRole(child.Node)
		if role == "x-translation" || role == "x-roman" {
			continue
		}
		builder.WriteString(ttmlMainText(child.Node))
	}
	return builder.String()
}

func ttmlHasTimedDescendant(node *ttmlNode) bool {
	if node == nil {
		return false
	}
	for _, child := range node.Children {
		if child.Node == nil {
			continue
		}
		if ttmlAttribute(child.Node, "begin") != "" ||
			ttmlAttribute(child.Node, "end") != "" ||
			ttmlHasTimedDescendant(child.Node) {
			return true
		}
	}
	return false
}

func collectTTMLMainContent(node *ttmlNode) ([]string, []LyricWord) {
	var parts []string
	var words []LyricWord
	if node == nil {
		return parts, words
	}

	for _, child := range node.Children {
		if child.Node == nil {
			if text := normalizeTTMLText(child.Text); text != "" {
				parts = append(parts, text)
			}
			continue
		}

		if role := ttmlRole(child.Node); role == "x-translation" || role == "x-roman" {
			continue
		}

		begin, hasBegin := parseTTMLTime(ttmlAttribute(child.Node, "begin"))
		end, hasEnd := parseTTMLTime(ttmlAttribute(child.Node, "end"))
		text := ttmlMainText(child.Node)
		if hasBegin && text != "" && !ttmlHasTimedDescendant(child.Node) {
			word := LyricWord{Time: begin, EndTime: math.NaN(), Text: text}
			if hasEnd {
				word.EndTime = end
			}
			words = append(words, word)
			parts = append(parts, text)
			continue
		}

		nestedParts, nestedWords := collectTTMLMainContent(child.Node)
		parts = append(parts, nestedParts...)
		words = append(words, nestedWords...)
	}
	return parts, words
}

func collectTTMLParagraphs(node *ttmlNode, paragraphs *[]*ttmlNode) {
	if node == nil {
		return
	}
	if node.Name == "p" {
		*paragraphs = append(*paragraphs, node)
		return
	}
	for _, child := range node.Children {
		collectTTMLParagraphs(child.Node, paragraphs)
	}
}

func parseTTMLLyrics(source string) ([]LyricLine, error) {
	document, err := parseTTMLDocument(source)
	if err != nil {
		return nil, err
	}
	paragraphs := make([]*ttmlNode, 0)
	collectTTMLParagraphs(document, &paragraphs)
	lines := make([]LyricLine, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		start, ok := parseTTMLTime(ttmlAttribute(paragraph, "begin"))
		if !ok {
			continue
		}
		end, hasEnd := parseTTMLTime(ttmlAttribute(paragraph, "end"))
		parts, words := collectTTMLMainContent(paragraph)
		text := strings.TrimSpace(strings.Join(parts, ""))
		if text == "" && len(words) > 0 {
			var builder strings.Builder
			for _, word := range words {
				builder.WriteString(word.Text)
			}
			text = strings.TrimSpace(builder.String())
		}
		if text == "" {
			continue
		}

		line := LyricLine{Time: start, EndTime: math.NaN(), Text: text, Words: words}
		if hasEnd {
			line.EndTime = end
		}
		lines = append(lines, line)
	}
	return normalizeLyricLines(lines), nil
}

type lyricsFileWord struct {
	Text    string      `yaml:"text"`
	StartMS interface{} `yaml:"start_ms"`
	EndMS   interface{} `yaml:"end_ms"`
}

type lyricsFileLine struct {
	Text    string           `yaml:"text"`
	StartMS interface{}      `yaml:"start_ms"`
	EndMS   interface{}      `yaml:"end_ms"`
	Words   []lyricsFileWord `yaml:"words"`
}

type lyricsFileDocument struct {
	Plain string           `yaml:"plain"`
	Lines []lyricsFileLine `yaml:"lines"`
}

func yamlNumber(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	default:
		return 0, false
	}
}

// parseLyricsfile handles LRCLIB's YAML Lyricsfile format. It is kept as a
// separate parser because Lyricsfile carries explicit word start/end times,
// unlike ordinary LRC. The YAML dependency is intentionally limited to this
// stable boundary; the rest of the application still uses the common model.
func parseLyricsfile(source string) ([]LyricLine, error) {
	if strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("empty Lyricsfile")
	}
	var document lyricsFileDocument
	if err := yaml.Unmarshal([]byte(source), &document); err != nil {
		return nil, err
	}

	lines := make([]LyricLine, 0, len(document.Lines))
	for _, rawLine := range document.Lines {
		startMS, ok := yamlNumber(rawLine.StartMS)
		if !ok {
			continue
		}
		words := make([]LyricWord, 0, len(rawLine.Words))
		for _, rawWord := range rawLine.Words {
			wordStartMS, ok := yamlNumber(rawWord.StartMS)
			if !ok {
				continue
			}
			word := LyricWord{
				Time:    wordStartMS / 1000,
				EndTime: math.NaN(),
				Text:    rawWord.Text,
			}
			if wordEndMS, ok := yamlNumber(rawWord.EndMS); ok {
				word.EndTime = wordEndMS / 1000
			}
			words = append(words, word)
		}

		text := strings.TrimSpace(rawLine.Text)
		if text == "" && len(words) > 0 {
			var builder strings.Builder
			for _, word := range words {
				builder.WriteString(word.Text)
			}
			text = strings.TrimSpace(builder.String())
		}
		if text == "" {
			continue
		}

		line := LyricLine{
			Time:    startMS / 1000,
			EndTime: math.NaN(),
			Text:    text,
			Words:   words,
		}
		if endMS, ok := yamlNumber(rawLine.EndMS); ok {
			line.EndTime = endMS / 1000
		}
		lines = append(lines, line)
	}
	return normalizeLyricLines(lines), nil
}

func comparableLyricMetadata(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("“", "", "”", "", "\"", "", "'", "",
		"-", " ", "–", " ", "—", " ", ":", " ", "|", " ").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func lyricMetadataMatches(value string, candidates []string) bool {
	comparable := comparableLyricMetadata(value)
	if comparable == "" {
		return false
	}
	for _, candidate := range candidates {
		if comparable == candidate || strings.ReplaceAll(comparable, " ", "") == strings.ReplaceAll(candidate, " ", "") {
			return true
		}
	}
	return false
}

func lyricMetadataCandidates(title string, artists []string) []string {
	values := make([]string, 0, len(artists)*3+1)
	appendCandidate := func(value string) {
		value = comparableLyricMetadata(value)
		if value == "" {
			return
		}
		for _, existing := range values {
			if existing == value {
				return
			}
		}
		values = append(values, value)
	}
	appendCandidate(title)
	appendCandidate(cleanTrackTitle(title))
	for _, artist := range artists {
		artist = strings.TrimSpace(artist)
		appendCandidate(artist)
		appendCandidate(title + " - " + artist)
		appendCandidate(artist + " - " + title)
	}
	return values
}

func leadingLyricMetadataWordCount(words []LyricWord, candidates []string) int {
	prefix := ""
	matched := 0
	for i, word := range words {
		prefix += word.Text
		if lyricMetadataMatches(prefix, candidates) {
			matched = i + 1
		}
	}
	return matched
}

func metadataSuffixWordCount(words []LyricWord, start int) int {
	if start >= len(words) || !strings.HasPrefix(strings.TrimSpace(words[start].Text), "(") {
		return 0
	}
	depth := 0
	for i := start; i < len(words); i++ {
		text := words[i].Text
		depth += strings.Count(text, "(")
		depth -= strings.Count(text, ")")
		if depth <= 0 {
			return i + 1
		}
	}
	return 0
}

// stripLeadingLyricMetadata removes timed title/credit tokens occasionally
// returned before the actual lyrics by karaoke providers. It only removes an
// exact leading title/artist match, so a real lyric line containing the title
// later in the text is preserved.
func stripLeadingLyricMetadata(lines []LyricLine, title string, artists []string) []LyricLine {
	if len(lines) == 0 {
		return lines
	}
	candidates := lyricMetadataCandidates(title, artists)
	if len(candidates) == 0 {
		return lines
	}

	for {
		first := -1
		for i, line := range lines {
			if strings.TrimSpace(line.Text) != "" || len(line.Words) > 0 {
				first = i
				break
			}
		}
		if first < 0 {
			break
		}

		line := &lines[first]
		if len(line.Words) > 0 {
			matched := leadingLyricMetadataWordCount(line.Words, candidates)
			if matched > 0 {
				if suffix := metadataSuffixWordCount(line.Words, matched); suffix > matched {
					matched = suffix
				}
				line.Words = append([]LyricWord(nil), line.Words[matched:]...)
				if len(line.Words) == 0 {
					lines = append(lines[:first], lines[first+1:]...)
					continue
				}
				var builder strings.Builder
				for _, word := range line.Words {
					builder.WriteString(word.Text)
				}
				line.Text = strings.TrimSpace(builder.String())
				line.Time = line.Words[0].Time
				continue
			}
		}
		if !lyricMetadataMatches(line.Text, candidates) {
			break
		}
		lines = append(lines[:first], lines[first+1:]...)
	}
	return normalizeLyricLines(lines)
}

// renderKaraokeLine colors completed words with the accent color, the active
// word with the primary color, and upcoming words with the subdued color.
// This mirrors the reference shell's word-level state without requiring a
// terminal-specific animation primitive.
func renderKaraokeLine(line LyricLine, position float64, theme Theme) string {
	if len(line.Words) == 0 {
		return theme.Primary + line.Text + theme.Reset
	}

	currentWord := -1
	for i, word := range line.Words {
		if position >= word.Time {
			currentWord = i
		} else {
			break
		}
	}

	var builder strings.Builder
	for i, word := range line.Words {
		color := theme.Gray
		if i < currentWord {
			color = theme.Accent
		} else if i == currentWord {
			color = theme.Primary
		}
		builder.WriteString(color)
		builder.WriteString(word.Text)
	}
	builder.WriteString(theme.Reset)
	return builder.String()
}

func mapStringValue(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				return text
			}
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case float32:
			return strconv.FormatFloat(float64(typed), 'f', -1, 32)
		case int:
			return strconv.Itoa(typed)
		case []interface{}:
			for _, item := range typed {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text)
				}
			}
		}
	}
	return ""
}

func mapFloatValue(values map[string]interface{}, keys ...string) float64 {
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed
		case float32:
			return float64(typed)
		case int:
			return float64(typed)
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err == nil {
				return parsed
			}
		}
	}
	return math.NaN()
}

func hasWordSyncedLyrics(lines []LyricLine) bool {
	for _, line := range lines {
		if len(line.Words) > 0 {
			return true
		}
	}
	return false
}

func lyricResultFromMap(values map[string]interface{}, source string, quality int) *lyricResult {
	if values == nil {
		return nil
	}
	synced := mapStringValue(values, "syncedLyrics", "synced")
	plain := mapStringValue(values, "plainLyrics", "plain")
	lines := parseSyncedLyrics(synced)
	resultSource := source
	lyricsfile := mapStringValue(values, "lyricsfile")
	if lyricsfile != "" {
		if fileLines, err := parseLyricsfile(lyricsfile); err == nil && len(fileLines) > 0 {
			lines = fileLines
			resultSource = "lrclib-lyricsfile"
			if quality < 540 {
				quality = 540
			}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	if hasWordSyncedLyrics(lines) && quality < 500 {
		quality = 500
	}
	return &lyricResult{
		Title:    mapStringValue(values, "trackName", "track", "title", "name", "musicName"),
		Artist:   mapStringValue(values, "artistName", "artist", "artists", "artistNames"),
		Album:    mapStringValue(values, "albumName", "album", "albumNames"),
		Duration: mapFloatValue(values, "duration"),
		Lines:    lines,
		Synced:   synced,
		Plain:    plain,
		Source:   resultSource,
		Quality:  quality,
	}
}

func lyricResultMatchScore(result *lyricResult, targetTitle string, targetArtists []string, targetAlbum string) float64 {
	if result == nil {
		return -1
	}
	titleScore := 0.5
	if result.Title != "" {
		titleScore = titleSimilarity(result.Title, targetTitle)
	}
	artistScore := 0.5
	if result.Artist != "" {
		artistScore = bestSimilarityAgainst(result.Artist, targetArtists)
	}
	albumScore := 0.0
	if targetAlbum != "" && result.Album != "" {
		albumScore = titleSimilarity(result.Album, targetAlbum)
	}
	return titleScore*5 + artistScore*3 + albumScore
}

func lyricResultDurationDifference(result *lyricResult, targetDuration int) float64 {
	if result == nil || math.IsNaN(result.Duration) || math.IsInf(result.Duration, 0) ||
		result.Duration <= 0 || targetDuration <= 0 {
		return math.Inf(1)
	}
	return math.Abs(result.Duration - float64(targetDuration))
}

func betterLyricResult(candidate, current *lyricResult, targetDuration int, targetTitle string, targetArtists []string, targetAlbum string) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	candidateMatch := lyricResultMatchScore(candidate, targetTitle, targetArtists, targetAlbum)
	currentMatch := lyricResultMatchScore(current, targetTitle, targetArtists, targetAlbum)
	if candidateMatch > currentMatch+1.2 {
		return true
	}
	if math.Abs(candidateMatch-currentMatch) > 1.2 {
		return false
	}
	if candidate.Quality != current.Quality {
		return candidate.Quality > current.Quality
	}
	candidateDuration := lyricResultDurationDifference(candidate, targetDuration)
	currentDuration := lyricResultDurationDifference(current, targetDuration)
	if candidateDuration < currentDuration-0.5 {
		return true
	}
	if math.Abs(candidateDuration-currentDuration) > 0.5 {
		return false
	}
	if len(candidate.Lines) != len(current.Lines) {
		return len(candidate.Lines) > len(current.Lines)
	}
	return countLyricWords(candidate.Lines) > countLyricWords(current.Lines)
}

func countLyricWords(lines []LyricLine) int {
	count := 0
	for _, line := range lines {
		count += len(line.Words)
	}
	return count
}

// applyLyricsResult は取得元に関係なく共通モデルを実際の表示状態へ反映する。
// リクエストが古くなっていれば(曲が切り替わっていれば)何もしない。
func applyLyricsResult(result *lyricResult, myReqID int) {
	lyricMutex.Lock()
	if currentReqID != myReqID {
		lyricMutex.Unlock()
		return
	}
	if result == nil {
		currentLyrics = nil
		lyricMutex.Unlock()
		return
	}
	if result.Artist != "" {
		currentDisplayArtist = result.Artist
	}
	currentLyrics = result.Lines
	lyricMutex.Unlock()
}

// getLyricsExact は lrclib の /api/get で track_name・artist_name・album_name・
// durationを指定した「厳密一致」の1件取得を試みる。
//
// /api/search は人気順・関連度順のランキング検索で、上位の一部しか返さない。
// そのため自分でアップロードしたばかりの曲のように母数の少ないエントリは、
// track_name・artist_nameが完全に合っていても検索結果に出てこないことがある。
// /api/get はMPRISのように track_name/artist_name/album_name/duration が
// 最初から全部分かっている場合向けの、ランキングを介さない直接取得なので、
// この用途ではむしろこちらが本筋。
func getLyricsExact(client *http.Client, title, artist, album string, durationSec int) (map[string]interface{}, bool) {
	params := url.Values{
		"track_name":  {title},
		"artist_name": {artist},
		"album_name":  {album},
		"duration":    {strconv.Itoa(durationSec)},
	}
	resp, err := lrclibHTTPGet(client, "https://lrclib.net/api/get?"+params.Encode())
	if err != nil {
		debugf("  exact-get HTTP error (title=%q artist=%q): %v", title, artist, err)
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		debugf("  exact-get status %d (title=%q artist=%q album=%q duration=%d)", resp.StatusCode, title, artist, album, durationSec)
		return nil, false
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		debugf("  exact-get decode error (title=%q artist=%q): %v", title, artist, err)
		return nil, false
	}
	synced := mapStringValue(result, "syncedLyrics", "synced")
	lyricsfile := mapStringValue(result, "lyricsfile")
	if synced == "" && (lyricsfile == "" || func() bool {
		lines, err := parseLyricsfile(lyricsfile)
		return err != nil || len(lines) == 0
	}()) {
		debugf("  exact-get hit but has no syncedLyrics (title=%q artist=%q)", title, artist)
		return nil, false
	}
	return result, true
}

// fetchLyricsFromLrclib は lrclib の検索レーンを担当する。
//
// artists は「1曲に紐づく全アーティスト名」のスライスで渡す。
// コラボ曲(例: "Imagine Dragons" + "Ado")の場合、以前は呼び出し側で
// あらかじめ1本の文字列(例: "Imagine Dragons; Ado")に結合してから
// 渡していたが、この結合文字列はlrclib/MusicBrainzのどちらにも
// 実在しないアーティスト名になるため、厳密一致・検索・MB解決・
// アーティスト類似度フィルタが軒並み機能しなくなり、無関係な曲の歌詞を
// 拾ってしまう不具合の原因になっていた。
// 各アーティストを個別の要素として持ち回すことで、この問題を解消する。
func fetchLyricsFromLrclib(title string, artists []string, album string, durationSec int) *lyricResult {
	debugf("lrclib fetch start: title=%q artists=%v album=%q duration=%ds", title, artists, album, durationSec)

	// インスト版は元々歌詞が存在しないので、無駄なHTTPリクエストを飛ばす前に、
	// かつ「似たタイトルの無関係な曲」を誤って引っ張ってくる前にここで弾く。
	if isInstrumentalTitle(title) {
		debugf("=> skipped: title matched instrumental pattern")
		return nil
	}

	client := http.Client{Timeout: 15 * time.Second}

	// "(feat. XXX)" のようなコラボ注釈を除いたタイトル。
	// lrclib側は注釈なしで登録されていることが多く、
	// 付いたままだと検索がヒットしなくなるため、こちらを優先的に使う。
	cleanTitle := cleanTrackTitle(title)
	if cleanTitle != title {
		debugf("cleaned title: %q -> %q", title, cleanTitle)
	}

	// artistsが空(メタデータが取れなかった等)の場合でも、
	// 空文字1件として扱えば以降のループ処理はそのまま動く。
	queryArtists := artists
	if len(queryArtists) == 0 {
		debugf("no artist metadata from playerctl, falling back to empty artist")
		queryArtists = []string{""}
	}

	// まず /api/get で厳密一致の直接取得を試す。個々のアーティスト名
	// (結合前の生の名前)ごとに試すことで、lrclib側がどのアーティストを
	// 主表記として登録していても拾えるようにする。
	for _, a := range queryArtists {
		if exact, ok := getLyricsExact(&client, cleanTitle, a, album, durationSec); ok {
			debugf("=> exact match via /api/get: query(title=%q artist=%q) -> lrclib id=%v artistName=%v albumName=%v duration=%v",
				cleanTitle, a, exact["id"], exact["artistName"], exact["albumName"], exact["duration"])
			return lyricResultFromMap(exact, "lrclib-exact", 390)
		}
		if cleanTitle != title {
			if exact, ok := getLyricsExact(&client, title, a, album, durationSec); ok {
				debugf("=> exact match via /api/get (uncleaned title): query(title=%q artist=%q) -> lrclib id=%v artistName=%v albumName=%v duration=%v",
					title, a, exact["id"], exact["artistName"], exact["albumName"], exact["duration"])
				return lyricResultFromMap(exact, "lrclib-exact", 390)
			}
		}
	}
	debugf("no exact match via /api/get, falling back to /api/search")

	var allResults []map[string]interface{}

	// 1段目: クリーンタイトル + 各アーティスト名
	for _, a := range queryArtists {
		res := searchLyrics(&client, url.Values{
			"track_name": {cleanTitle}, "artist_name": {a},
		})
		debugf("stage1 search track_name=%q artist_name=%q -> %d results", cleanTitle, a, len(res))
		allResults = append(allResults, res...)
	}

	// 2段目: クリーンタイトル + アルバム名（アーティスト表記が違う場合の保険）
	if album != "" {
		res := searchLyrics(&client, url.Values{
			"track_name": {cleanTitle}, "album_name": {album},
		})
		debugf("stage2 search track_name=%q album_name=%q -> %d results", cleanTitle, album, len(res))
		allResults = append(allResults, res...)
	}

	// 3段目: クリーンタイトルのみ（アーティスト名がローマ字/現地語などで一致しない場合の保険）
	res3 := searchLyrics(&client, url.Values{
		"track_name": {cleanTitle},
	})
	debugf("stage3 search track_name=%q -> %d results", cleanTitle, len(res3))
	allResults = append(allResults, res3...)

	// 4段目: 元のタイトルそのまま（逆に feat. 込みで登録されているレアケースの保険）
	if cleanTitle != title {
		res4 := searchLyrics(&client, url.Values{
			"track_name": {title},
		})
		debugf("stage4 search track_name=%q (uncleaned) -> %d results", title, len(res4))
		allResults = append(allResults, res4...)
	}

	// 5段目: MusicBrainzで正規化したタイトル・アーティストの別名義で検索。
	// "かめりあ"表記のトラックを"Camellia"名義でlrclibに登録している、
	// といった表記ゆれをここで吸収する。各アーティストで試し、
	// 最初に解決できたものを採用する。
	// ここで集めた別名義は、後段のpickBestMatchで「本当にこのアーティストか」を
	// 判定するための許容リストとしても使う。
	targetArtists := append([]string{}, artists...)
	for _, a := range queryArtists {
		if a == "" {
			continue
		}
		mbTitle, mbArtists, ok := mbResolve(&client, cleanTitle, a)
		if !ok {
			continue
		}
		debugf("musicbrainz resolved artist=%q -> title=%q aliases=%v", a, mbTitle, mbArtists)
		targetArtists = append(targetArtists, mbArtists...)
		for _, ma := range mbArtists {
			res5 := searchLyrics(&client, url.Values{
				"track_name": {mbTitle}, "artist_name": {ma},
			})
			debugf("stage5 search track_name=%q artist_name=%q -> %d results", mbTitle, ma, len(res5))
			allResults = append(allResults, res5...)
		}
		break
	}

	debugf("total raw results collected: %d", len(allResults))

	if len(allResults) == 0 {
		debugf("=> giving up: no results from any search stage")
		return nil
	}

	// 表記ゆれで違う曲がヒットしていても、再生時間が最も近いものを選ぶ。
	// ただしタイトルが同じでもアーティストが全く違う曲(例: ありふれた
	// タイトルの同名異曲)は targetArtists との類似度チェックで弾かれる。
	debugf("evaluating %d candidates against title=%q artists=%v", len(allResults), cleanTitle, targetArtists)
	best := pickBestMatch(allResults, durationSec, cleanTitle, targetArtists)
	if best == nil {
		debugf("=> giving up: every candidate was rejected by the similarity filters")
		return nil
	}
	debugf("=> selected: lrclib id=%v track=%v artist=%v album=%v", best["id"], best["trackName"], best["artistName"], best["albumName"])

	return lyricResultFromMap(best, "lrclib-search", 390)
}

const (
	syncLRCURL        = "https://api.synclrc.dev/lyrics"
	amllSearchURL     = "https://api.amll.dev/v1/lyrics/search"
	amllGetURL        = "https://api.amll.dev/v1/lyrics/get"
	providerUserAgent = "go-music-tui/1.0 (https://github.com/dot-1245/go-music-tui)"
)

func requestJSON(client *http.Client, requestURL string) (interface{}, int, error) {
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", providerUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	var payload interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, resp.StatusCode, err
	}
	return payload, resp.StatusCode, nil
}

func responseData(payload interface{}) interface{} {
	if values, ok := payload.(map[string]interface{}); ok {
		if data, exists := values["data"]; exists {
			return data
		}
	}
	return payload
}

func responseItems(payload interface{}) []map[string]interface{} {
	payload = responseData(payload)
	if items, ok := payload.([]interface{}); ok {
		result := make([]map[string]interface{}, 0, len(items))
		for _, item := range items {
			if values, ok := item.(map[string]interface{}); ok {
				result = append(result, values)
			}
		}
		return result
	}
	if values, ok := payload.(map[string]interface{}); ok {
		for _, key := range []string{"items", "results"} {
			if nested, exists := values[key]; exists {
				return responseItems(nested)
			}
		}
	}
	return nil
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func syncLRCArtistPlan(rawArtist string, artists []string) []string {
	plan := make([]string, 0, len(artists)+1)
	plan = appendUniqueString(plan, rawArtist)
	for _, artist := range artists {
		plan = appendUniqueString(plan, artist)
	}
	return plan
}

func lyricResultFromFields(values map[string]interface{}, lines []LyricLine, synced, plain, source string, quality int) *lyricResult {
	if len(lines) == 0 {
		return nil
	}
	if hasWordSyncedLyrics(lines) && quality < 500 {
		quality = 500
	}
	return &lyricResult{
		Title:    mapStringValue(values, "trackName", "track", "title", "name", "musicName"),
		Artist:   mapStringValue(values, "artistName", "artist", "artists", "artistNames"),
		Album:    mapStringValue(values, "albumName", "album", "albumNames"),
		Duration: mapFloatValue(values, "duration"),
		Lines:    lines,
		Synced:   strings.TrimSpace(synced),
		Plain:    strings.TrimSpace(plain),
		Source:   source,
		Quality:  quality,
	}
}

func syncLRCResult(payload interface{}) *lyricResult {
	values, ok := responseData(payload).(map[string]interface{})
	if !ok {
		return nil
	}

	karaoke := mapStringValue(values, "karaoke")
	synced := mapStringValue(values, "synced")
	plain := mapStringValue(values, "plain")
	returnedLyrics := mapStringValue(values, "lyrics")
	returnedType := strings.ToLower(mapStringValue(values, "type"))
	if returnedLyrics != "" {
		switch {
		case returnedType == "karaoke" || lyricWordTagRe.MatchString(returnedLyrics):
			karaoke = returnedLyrics
		case returnedType == "synced" || lyricLineTagRe.MatchString(returnedLyrics):
			synced = returnedLyrics
		default:
			plain = returnedLyrics
		}
	}

	if karaoke != "" {
		lines := parseSyncedLyrics(karaoke)
		if len(lines) > 0 {
			if hasWordSyncedLyrics(lines) {
				return lyricResultFromFields(values, lines, karaoke, plain, "synclrc-enhanced", 600)
			}
			synced = karaoke
		}
	}
	if synced != "" {
		lines := parseSyncedLyrics(synced)
		if len(lines) > 0 {
			return lyricResultFromFields(values, lines, synced, plain, "synclrc-synced", 300)
		}
	}
	return nil
}

func fetchSyncLRCLyrics(client *http.Client, title, rawArtist string, artists []string, album string, durationSec int) *lyricResult {
	for _, artist := range syncLRCArtistPlan(rawArtist, artists) {
		params := url.Values{
			"track":  {title},
			"artist": {artist},
			"album":  {album},
			"type":   {"karaoke"},
		}
		if durationSec > 0 {
			params.Set("duration", strconv.Itoa(durationSec))
		}
		payload, _, err := requestJSON(client, syncLRCURL+"?"+params.Encode())
		if err != nil {
			debugf("synclrc request failed (title=%q artist=%q): %v", title, artist, err)
			continue
		}
		if result := syncLRCResult(payload); result != nil {
			debugf("synclrc result: source=%s artist=%q", result.Source, result.Artist)
			return result
		}
	}
	return nil
}

func amllCandidateScore(candidate map[string]interface{}, title string, artists []string, album string) (float64, bool) {
	candidateTitle := mapStringValue(candidate, "musicName", "musicNames", "trackName", "title", "name")
	candidateArtist := mapStringValue(candidate, "artistName", "artist", "artistNames", "artists")
	if candidateTitle != "" && titleSimilarity(candidateTitle, title) < 0.45 {
		return 0, false
	}
	if candidateArtist != "" && bestSimilarityAgainst(candidateArtist, artists) < 0.4 {
		return 0, false
	}

	titleScore := 0.5
	if candidateTitle != "" {
		titleScore = titleSimilarity(candidateTitle, title)
	}
	artistScore := 0.5
	if candidateArtist != "" {
		artistScore = bestSimilarityAgainst(candidateArtist, artists)
	}
	albumScore := 0.0
	candidateAlbum := mapStringValue(candidate, "albumName", "album", "albumNames")
	if album != "" && candidateAlbum != "" {
		albumScore = titleSimilarity(candidateAlbum, album)
	}
	return titleScore*5 + artistScore*3 + albumScore, true
}

func selectAMLLCandidate(candidates []map[string]interface{}, title string, artists []string, album string) map[string]interface{} {
	var best map[string]interface{}
	bestScore := -1.0
	for _, candidate := range candidates {
		score, ok := amllCandidateScore(candidate, title, artists, album)
		if ok && score > bestScore {
			best = candidate
			bestScore = score
		}
	}
	return best
}

func amllArtistPlan(rawArtist string, artists []string) []string {
	plan := make([]string, 0, len(artists)+1)
	plan = appendUniqueString(plan, rawArtist)
	for _, artist := range artists {
		plan = appendUniqueString(plan, artist)
	}
	return plan
}

func amllSearchPlan(title, rawArtist string, artists []string, album string) []string {
	titles := []string{cleanTrackTitle(title)}
	if titles[0] != title {
		titles = append(titles, title)
	}
	artistPlan := amllArtistPlan(rawArtist, artists)
	plan := make([]string, 0)
	appendPlan := func(parameters url.Values) {
		requestURL := amllSearchURL + "?" + parameters.Encode()
		for _, existing := range plan {
			if existing == requestURL {
				return
			}
		}
		plan = append(plan, requestURL)
	}

	for _, candidateTitle := range titles {
		if len(artistPlan) == 0 {
			appendPlan(url.Values{"musicName": {candidateTitle}})
			continue
		}
		for _, artist := range artistPlan {
			if album != "" {
				appendPlan(url.Values{
					"musicName":  {candidateTitle},
					"artistName": {artist},
					"albumName":  {album},
				})
			}
			appendPlan(url.Values{
				"musicName":  {candidateTitle},
				"artistName": {artist},
			})
		}
	}
	return plan
}

func copyMap(values map[string]interface{}) map[string]interface{} {
	copyOf := make(map[string]interface{}, len(values))
	for key, value := range values {
		copyOf[key] = value
	}
	return copyOf
}

func mergeAMLLMetadata(candidate, payload map[string]interface{}) map[string]interface{} {
	merged := copyMap(candidate)
	metadata, _ := payload["metadata"].(map[string]interface{})
	if metadata == nil {
		return merged
	}
	for target, sources := range map[string][]string{
		"musicName":  {"musicName", "trackName", "title"},
		"artistName": {"artistName", "artist"},
		"albumName":  {"albumName", "album"},
		"duration":   {"duration"},
	} {
		if _, exists := merged[target]; exists && mapStringValue(merged, target) != "" {
			continue
		}
		for _, source := range sources {
			if value, exists := metadata[source]; exists {
				merged[target] = value
				break
			}
		}
	}
	return merged
}

func fetchAMLLLyrics(client *http.Client, title, rawArtist string, artists []string, album string, durationSec int) *lyricResult {
	searchPlan := amllSearchPlan(title, rawArtist, artists, album)
	for _, searchURL := range searchPlan {
		payload, _, err := requestJSON(client, searchURL)
		if err != nil {
			debugf("amll search failed (%s): %v", searchURL, err)
			continue
		}
		candidate := selectAMLLCandidate(responseItems(payload), title, artists, album)
		if candidate == nil {
			continue
		}

		id := mapStringValue(candidate, "id")
		filename := mapStringValue(candidate, "filename")
		if id == "" && filename == "" {
			continue
		}
		parameters := url.Values{}
		if id != "" {
			parameters.Set("id", id)
		} else {
			parameters.Set("filename", filename)
		}
		getURL := amllGetURL + "?" + parameters.Encode()
		getPayload, _, err := requestJSON(client, getURL)
		if err != nil {
			debugf("amll get failed (%s): %v", getURL, err)
			continue
		}
		data, _ := responseData(getPayload).(map[string]interface{})
		if data == nil {
			continue
		}
		ttml := mapStringValue(data, "lyrics")
		if ttml == "" {
			continue
		}
		lines, err := parseTTMLLyrics(ttml)
		if err != nil || len(lines) == 0 {
			debugf("amll TTML parse failed (title=%q): %v", title, err)
			continue
		}

		metadata := mergeAMLLMetadata(candidate, data)
		plain := make([]string, 0, len(lines))
		for _, line := range lines {
			plain = append(plain, line.Text)
		}
		quality := 450
		source := "amll-ttml"
		if hasWordSyncedLyrics(lines) {
			quality = 650
			source = "amll-ttml-word"
		}
		debugf("amll result: source=%s title=%q artist=%q", source,
			mapStringValue(metadata, "musicName", "trackName", "title"),
			mapStringValue(metadata, "artistName", "artist"))
		return lyricResultFromFields(metadata, lines, "", strings.Join(plain, "\n"), source, quality)
	}
	return nil
}

// fetchLyricsAsync starts all lyric providers for the current track at once.
// The first usable result can be shown immediately, while a later
// word-synchronized result is allowed to replace it. Every application still
// goes through the request ID check, so a slow previous track cannot update
// the new one.
func fetchLyricsAsync(title string, artists []string, rawArtist string, album string, durationSec int, myReqID int) {
	go func() {
		debugf("=== fetch start: title=%q artists=%v rawArtist=%q album=%q duration=%ds (reqID=%d)", title, artists, rawArtist, album, durationSec, myReqID)
		if isInstrumentalTitle(title) {
			debugf("=> skipped: title matched instrumental pattern")
			applyLyricsResult(nil, myReqID)
			return
		}

		targetArtists := make([]string, 0, len(artists)+1)
		targetArtists = appendUniqueString(targetArtists, rawArtist)
		for _, artist := range artists {
			targetArtists = appendUniqueString(targetArtists, artist)
		}

		client := &http.Client{Timeout: 15 * time.Second}
		results := make(chan *lyricResult, 3)
		go func() {
			results <- fetchLyricsFromLrclib(title, artists, album, durationSec)
		}()
		go func() {
			results <- fetchSyncLRCLyrics(client, title, rawArtist, artists, album, durationSec)
		}()
		go func() {
			results <- fetchAMLLLyrics(client, title, rawArtist, artists, album, durationSec)
		}()

		var best *lyricResult
		completed := 0
		timeout := time.NewTimer(2800 * time.Millisecond)
		defer timeout.Stop()
		timeoutCh := timeout.C
		for completed < 3 {
			select {
			case result := <-results:
				completed++
				if result != nil {
					result.Lines = stripLeadingLyricMetadata(result.Lines, title, targetArtists)
					if len(result.Lines) == 0 {
						result = nil
					}
				}
				if result != nil && betterLyricResult(result, best, durationSec, cleanTrackTitle(title), targetArtists, album) {
					best = result
					debugf("=> applied provider result: source=%s title=%q artist=%q (reqID=%d)", result.Source, result.Title, result.Artist, myReqID)
					applyLyricsResult(result, myReqID)
				}
			case <-timeoutCh:
				// Match the reference implementation's short collection window:
				// an unavailable provider must not keep the UI in Loading forever.
				timeoutCh = nil
				if best == nil {
					applyLyricsResult(nil, myReqID)
				}
			}
		}
		if best == nil {
			applyLyricsResult(nil, myReqID)
		}
	}()
}

func main() {
	flag.Parse()
	if *flagNoInfo && *flagNoLyrics && *flagNoArt {
		fmt.Fprintln(os.Stderr, "--noinfo --nolyrics --noart を同時に指定することはできません")
		os.Exit(1)
	}

	initDebugLog()
	if debugLogFile != nil {
		defer debugLogFile.Close()
	}
	debugf("==== go-music-tui started (noinfo=%v nolyrics=%v noart=%v) ====", *flagNoInfo, *flagNoLyrics, *flagNoArt)

	// アートのみ / 歌詞のみ表示の場合は、端末いっぱいに広げて表示する
	isArtOnly := *flagNoInfo && *flagNoLyrics && !*flagNoArt
	isLyricsOnly := *flagNoInfo && *flagNoArt && !*flagNoLyrics

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
	var prevCols, prevRows int // リサイズ検知用

	// バッファを持たせることで、キー入力の読み取りgoroutineがメインループの
	// 処理待ちでブロックしにくくする（長押し時の入力詰まり対策の一部）。
	inputChan := make(chan byte, 64)
	go func() {
		buf := make([]byte, 1)
		for {
			os.Stdin.Read(buf)
			inputChan <- buf[0]
		}
	}()

	for {
		cols, rows, _ := term.GetSize(int(os.Stdout.Fd()))

		// ターミナルのサイズが変わった時だけ全画面クリアする。
		// 通常のフレームは各行を \033[K で部分クリアしているだけで
		// 全画面クリアはしていないため、alt-screenバッファ上には
		// リサイズ前の古い行(特にfooterのテキスト)が消されずに
		// 残ってしまうことがある。それがターミナルを大きくした時に
		// 「復活」して見えて、footer行が増殖したように見えるバグの原因。
		if cols != prevCols || rows != prevRows {
			fmt.Print("\033[2J")
			lastArtUrl = "" // ジャケット画像もサイズが変わるので再描画させる
			prevCols, prevRows = cols, rows
		}

		if stat, err := os.Stat(themePath); err == nil {
			if stat.ModTime().After(lastThemeModTime) {
				theme = loadTheme()
				lastThemeModTime = stat.ModTime()
			}
		}

		pList := cmdOut("-l")
		hasPlayer := pList != ""
		var p string
		if hasPlayer {
			p = strings.Fields(pList)[0]
		}

		// キー長押し対策: 1ループにつき1個だけ処理するのではなく、
		// その時点で溜まっている入力を全部処理してから次に進む。
		// こうしないとオートリピートで溜まったキューの後ろにESCが並んでしまい、
		// 長押しをやめるまで数秒〜操作不能になる。
		//
		// プレイヤーが見つからない場合でも、ESC/Ctrl+Cによる終了操作だけは
		// 必ず効くようにするため、この処理は「プレイヤーが無ければcontinue」
		// より前に置いている。以前はプレイヤー未検出時にここへ到達する前に
		// continueしてしまい、音楽を止めている間は何のキーも一切効かない
		// （終了すらできない）状態になっていた。
	drainInput:
		for {
			select {
			case key := <-inputChan:
				switch key {
				case 27, 3:
					return
				}
				if !hasPlayer {
					continue
				}
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
				}
			default:
				break drainInput
			}
		}

		if !hasPlayer {
			fmt.Print("\033[H\033[K 󰝛 No player found.")
			time.Sleep(1 * time.Second)
			continue
		}

		metaOut := cmdOut("-p", p, "metadata", "--format", "{{position}};;{{mpris:length}};;{{volume}};;{{status}};;{{xesam:title}};;{{xesam:artist}};;{{xesam:album}};;{{mpris:artUrl}};;{{shuffle}};;{{loop}}")
		parts := strings.Split(metaOut, ";;")

		if len(parts) < 10 {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		posF, _ := strconv.ParseFloat(parts[0], 64)
		posSec := posF / 1000000.0
		lenI, _ := strconv.Atoi(parts[1])
		volF, _ := strconv.ParseFloat(parts[2], 64)

		info := PlayerInfo{
			Name: p, Position: int(posSec), Length: lenI / 1000000, Volume: int(volF * 100),
			Status: parts[3], Title: parts[4], Artist: parts[5], Album: parts[6],
			ArtUrl: parts[7], Shuffle: parts[8], Loop: parts[9],
		}

		if info.Title != prevTitle || info.Artist != prevArtist {
			debugf("track changed: %q/%q -> %q/%q", prevTitle, prevArtist, info.Title, info.Artist)
			theme = loadTheme()

			if !*flagNoLyrics {
				lyricMutex.Lock()
				currentReqID++
				activeID := currentReqID
				currentLyrics = []LyricLine{{Time: 0, Text: "Loading lyrics..."}}
				currentDisplayArtist = info.Artist
				lyricMutex.Unlock()

				// xesam:artist を個別の値のまま取得する。--format で結合された
				// info.Artist (例: "Imagine Dragons; Ado") をそのまま検索に
				// 使うと、実在しないアーティスト名として扱われ検索が軒並み
				// 失敗する。取得できない場合のみ、結合済み文字列を分割した
				// ものでフォールバックする。
				artists := getArtistList(p)
				if len(artists) == 0 {
					artists = splitArtistsFallback(info.Artist)
				}
				// プレイヤーがxesam:artistを1要素にカンマ結合したまま公開している
				// ケースがあるため、ここでもう一段バラす(flattenArtistsのコメント参照)。
				beforeFlatten := artists
				artists = flattenArtists(artists)
				if len(artists) != len(beforeFlatten) {
					debugf("artists flattened: %v -> %v", beforeFlatten, artists)
				}

				// 歌詞取得はMPRISのtitle/artist/albumメタデータだけを見ており、
				// プレイヤー固有の処理は無いので、プレイヤー名によるホワイトリストは
				// 本来不要（Feishinなどspotify/mpv以外のプレイヤーも普通に動く）。
				// タイトルが取れていない場合だけ諦める。
				if info.Title != "" {
					fetchLyricsAsync(info.Title, artists, info.Artist, info.Album, info.Length, activeID)
				} else {
					lyricMutex.Lock()
					currentLyrics = nil
					lyricMutex.Unlock()
				}
			} else {
				// --nolyrics 時は取得処理自体を丸ごとスキップする。
				// Info欄の Artist 表示だけは更新しておく。
				lyricMutex.Lock()
				currentDisplayArtist = info.Artist
				lyricMutex.Unlock()
			}

			prevTitle = info.Title
			prevArtist = info.Artist
		}

		if !*flagNoArt && info.ArtUrl != lastArtUrl {
			fmt.Print("\x1b_Ga=d\x1b\\")
			if info.ArtUrl != "" {
				if img, err := fetchImage(info.ArtUrl); err == nil {
					imgSize := uint(250)
					if rows < 25 {
						imgSize = 180
					}
					posRow, posCol := 2, 2

					if isArtOnly {
						bounds := img.Bounds()
						srcW, srcH := bounds.Dx(), bounds.Dy()
						if srcW > 0 && srcH > 0 {
							if xpx, ypx, ok := getTermPixelSize(); ok && cols > 0 && rows > 0 {
								// 端末の実ピクセルサイズが取れる場合は、
								// アスペクト比を保ったまま画面いっぱいに
								// 収まる最大サイズを計算し、中央に配置する。
								scale := float64(xpx) / float64(srcW)
								if s := float64(ypx) / float64(srcH); s < scale {
									scale = s
								}
								imgSize = uint(float64(srcW) * scale)

								cellW := float64(xpx) / float64(cols)
								cellH := float64(ypx) / float64(rows)
								if cellW > 0 && cellH > 0 {
									dispCellsW := float64(imgSize) / cellW
									dispCellsH := (float64(srcH) * scale) / cellH
									posCol = int(float64(cols)/2-dispCellsW/2) + 1
									posRow = int(float64(rows)/2-dispCellsH/2) + 1
									if posCol < 1 {
										posCol = 1
									}
									if posRow < 1 {
										posRow = 1
									}
								}
							} else {
								// ピクセルサイズを報告しない端末向けの簡易フォールバック
								imgSize = uint(cols * 9)
							}
						}
					}

					resized := resize.Resize(imgSize, 0, img, resize.Lanczos3)
					fmt.Printf("\033[%d;%dH", posRow, posCol)
					kittyimg.Fprintln(os.Stdout, resized)
					lastArtUrl = info.ArtUrl
				} else {
					// 取得失敗時もこのURLは処理済み扱いにする。
					// ここを "" のままにすると次のループでも同じURLが
					// 「未取得」と判定され続け、毎フレーム同じ画像を
					// 再リクエストし続けてしまう。
					lastArtUrl = info.ArtUrl
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

		// --noart 時はアルバムアート用の余白が不要になるので、
		// 左端から使うようにする。
		offsetX := 40
		if rows < 25 {
			offsetX = 32
		}
		if *flagNoArt {
			offsetX = 4
		}

		draw := func(y int, color, icon, label, text string) {
			limit := cols - offsetX - 10
			if limit > 0 {
				// rune単位で切る。バイト単位(len(text))で切ると
				// 日本語や絵文字混じりの文字列でUTF-8境界を壊し、
				// 文字化けを起こすことがあるため。
				runes := []rune(text)
				if len(runes) > limit {
					text = string(runes[:limit])
				}
			}
			fmt.Printf("\033[%d;%dH%s%s %s%-8s: %s%s\033[K", y, offsetX, color, icon, theme.Gray, label, theme.Reset, text)
		}

		if !*flagNoInfo {
			draw(3, theme.Accent, "󰎈", "Status", info.Status)
			draw(5, theme.Primary, "󰎆", "Title", info.Title)
			draw(6, theme.SubText, "󰗡", "Artist", info.Artist)
			draw(7, theme.Gray, "󰀥", "Album", info.Album)
			draw(8, theme.Accent, "󰓇", "App", info.Name)

			draw(10, theme.Accent, "󰒝", "Shuffle", info.Shuffle)
			draw(11, theme.Accent, "󰑐", "Loop", info.Loop)

			volW := 12
			volP := info.Volume * volW / 100
			if volP > volW {
				volP = volW
			}
			if volP < 0 {
				volP = 0
			}
			volBar := theme.Accent + strings.Repeat("=", volP) + theme.Gray + strings.Repeat("-", volW-volP) + theme.Reset
			draw(12, theme.Accent, "󰕾", "Volume", fmt.Sprintf("[%s] %d%%", volBar, info.Volume))

			barW := cols - offsetX - 18
			if barW < 10 {
				barW = 10
			}
			prog := 0
			if info.Length > 0 {
				prog = info.Position * barW / info.Length
			}
			if prog > barW {
				prog = barW
			}
			if prog < 0 {
				prog = 0
			}

			barStr := theme.Accent + strings.Repeat("=", prog) + theme.Gray + strings.Repeat("-", barW-prog) + theme.Reset
			timeStr := fmt.Sprintf("%02d:%02d / %02d:%02d", info.Position/60, info.Position%60, info.Length/60, info.Length%60)
			fmt.Printf("\033[14;%dH%s  %s\033[K", offsetX, barStr, timeStr)
		}

		if !*flagNoLyrics {
			lyricMutex.Lock()
			lyricsSnapshot := currentLyrics
			lyricMutex.Unlock()

			// --noinfo 時は情報欄が無いので、歌詞を上に詰めて表示する。
			lyricY := 17
			if *flagNoInfo {
				lyricY = 4
			}

			currentIdx := -1
			nextIdx := -1
			for i, line := range lyricsSnapshot {
				if posSec >= line.Time {
					currentIdx = i
				} else {
					nextIdx = i
					break
				}
			}

			if isLyricsOnly {
				// 歌詞のみモード: 前後3行だけでなく、端末の縦幅いっぱいまで
				// 表示行を広げる。マイクアイコンは使わず、現在行を色だけで
				// ハイライトする。
				maxLines := rows - 2
				if maxLines < 1 {
					maxLines = 1
				}

				if len(lyricsSnapshot) == 0 {
					for row := 1; row <= maxLines; row++ {
						fmt.Printf("\033[%d;%dH\033[K", row, offsetX)
					}
				} else {
					activeIdx := currentIdx
					if activeIdx == -1 {
						activeIdx = 0
					}
					half := maxLines / 2
					start := activeIdx - half
					if start < 0 {
						start = 0
					}
					end := start + maxLines
					if end > len(lyricsSnapshot) {
						end = len(lyricsSnapshot)
						start = end - maxLines
						if start < 0 {
							start = 0
						}
					}

					row := 1
					for i := start; i < end; i++ {
						if i == currentIdx {
							fmt.Printf("\033[%d;%dH%s\033[K", row, offsetX,
								renderKaraokeLine(lyricsSnapshot[i], posSec, theme))
						} else {
							fmt.Printf("\033[%d;%dH%s%s\033[K", row, offsetX,
								theme.Gray, lyricsSnapshot[i].Text)
						}
						row++
					}
					for ; row <= maxLines; row++ {
						fmt.Printf("\033[%d;%dH\033[K", row, offsetX)
					}
				}
			} else if len(lyricsSnapshot) == 0 {
				// 未取得/取得失敗/インスト版などで歌詞が無い場合は、
				// "No lyrics found" のようなプレースホルダー文字は出さず、
				// 単に何も表示しない（該当行をクリアするだけ）。
				fmt.Printf("\033[%d;%dH\033[K", lyricY, offsetX)
				fmt.Printf("\033[%d;%dH\033[K", lyricY+1, offsetX)
				fmt.Printf("\033[%d;%dH\033[K", lyricY+2, offsetX)
			} else {
				if currentIdx == -1 {
					fmt.Printf("\033[%d;%dH\033[K", lyricY, offsetX)
					if nextIdx != -1 && nextIdx < len(lyricsSnapshot) {
						fmt.Printf("\033[%d;%dH%s🎤 %s\033[K", lyricY+1, offsetX, theme.Gray, lyricsSnapshot[nextIdx].Text)
					} else {
						fmt.Printf("\033[%d;%dH\033[K", lyricY+1, offsetX)
					}
					fmt.Printf("\033[%d;%dH\033[K", lyricY+2, offsetX)
				} else {
					currentText := renderKaraokeLine(lyricsSnapshot[currentIdx], posSec, theme)

					if currentIdx > 0 {
						fmt.Printf("\033[%d;%dH%s%s\033[K", lyricY, offsetX, theme.Gray, lyricsSnapshot[currentIdx-1].Text)
					} else {
						fmt.Printf("\033[%d;%dH\033[K", lyricY, offsetX)
					}
					fmt.Printf("\033[%d;%dH%s🎤 %s\033[K", lyricY+1, offsetX, theme.Primary, currentText)
					if currentIdx+1 < len(lyricsSnapshot) {
						fmt.Printf("\033[%d;%dH%s%s\033[K", lyricY+2, offsetX, theme.Gray, lyricsSnapshot[currentIdx+1].Text)
					} else {
						fmt.Printf("\033[%d;%dH\033[K", lyricY+2, offsetX)
					}
				}
			}
		}

		if !*flagNoInfo {
			fmt.Printf("\033[%d;2H%s[w/s] Vol | [q/e] Prev/Next | [a/d] Seek | [z/x] Shuffle/Loop | [Space] Toggle | [ESC] Quit%s\033[K", rows-1, theme.Gray, theme.Reset)
		}

		fmt.Print("\033[H")
		time.Sleep(100 * time.Millisecond)
	}
}
