package lyrics

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	featRe        = regexp.MustCompile(`(?i)[\(\[](feat\.?|ft\.?|with)\s+[^\)\]]*[\)\]]`)
	instRe        = regexp.MustCompile(`(?i)(?:[\(\[【]|^|\s[-–—]\s*)(inst(?:rumental)?\.?|off\s*vocal|karaoke|カラオケ|インスト(?:ゥルメンタル)?)(?:[\)\]】]|$)`)
	artistSplitRe = regexp.MustCompile(`\s*[,;]\s*|\s+/\s+|\s+(?:feat\.?|ft\.?|with|&)\s+`)
)

const (
	minTitleSimilarity  = 0.4
	minArtistSimilarity = 0.4
)

// CleanTrackTitle removes collaboration annotations used by some players.
func CleanTrackTitle(title string) string {
	return strings.TrimSpace(featRe.ReplaceAllString(title, ""))
}

// IsInstrumentalTitle reports whether a title indicates an instrumental track.
func IsInstrumentalTitle(title string) bool {
	return instRe.MatchString(title)
}

func levenshtein(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = prev[j] + 1
			if value := curr[j-1] + 1; value < curr[j] {
				curr[j] = value
			}
			if value := prev[j-1] + cost; value < curr[j] {
				curr[j] = value
			}
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// TitleSimilarity returns a rune-aware case-insensitive similarity from 0 to 1.
func TitleSimilarity(a, b string) float64 {
	ra := []rune(strings.ToLower(strings.TrimSpace(a)))
	rb := []rune(strings.ToLower(strings.TrimSpace(b)))
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	if maxLen == 0 {
		return 1
	}
	return 1 - float64(levenshtein(ra, rb))/float64(maxLen)
}

func splitArtistVariants(value string) []string {
	parts := artistSplitRe.Split(value, -1)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func normalizeArtistName(value string) string {
	value = strings.NewReplacer(",", " ", ";", " ", "&", " ", "/", " ").Replace(strings.ToLower(value))
	tokens := strings.Fields(value)
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

// BestSimilarityAgainst returns the best artist similarity against any target.
func BestSimilarityAgainst(candidate string, targets []string) float64 {
	if len(targets) == 0 {
		return 1
	}
	best := 0.0
	candidateVariants := append([]string{candidate}, splitArtistVariants(candidate)...)
	for _, candidateVariant := range candidateVariants {
		if normalizeArtistName(candidateVariant) == "" {
			continue
		}
		for _, target := range targets {
			targetVariants := append([]string{target}, splitArtistVariants(target)...)
			for _, targetVariant := range targetVariants {
				similarity := TitleSimilarity(normalizeArtistName(candidateVariant), normalizeArtistName(targetVariant))
				if similarity > best {
					best = similarity
				}
			}
		}
	}
	return best
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
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
				return parsed
			}
		}
	}
	return math.NaN()
}

// PickBestMatch chooses the closest valid LRCLIB search candidate.
func PickBestMatch(results []map[string]interface{}, targetDuration int, targetTitle string, targetArtists []string, logger Logger) map[string]interface{} {
	var best map[string]interface{}
	bestDiff := math.MaxFloat64
	bestHasDuration := false
	for _, result := range results {
		synced := mapStringValue(result, "syncedLyrics", "synced")
		lyricsfile := mapStringValue(result, "lyricsfile")
		if synced == "" && (lyricsfile == "" || func() bool {
			lines, err := ParseLyricsfile(lyricsfile)
			return err != nil || len(lines) == 0
		}()) {
			continue
		}

		trackName := mapStringValue(result, "trackName", "track", "title")
		artistName := mapStringValue(result, "artistName", "artist")
		if trackName != "" {
			similarity := TitleSimilarity(trackName, targetTitle)
			if similarity < minTitleSimilarity {
				logf(logger, "  reject %q / %q: title similarity %.2f < %.2f (target title=%q)", trackName, artistName, similarity, minTitleSimilarity, targetTitle)
				continue
			}
		}
		if artistName != "" {
			similarity := BestSimilarityAgainst(artistName, targetArtists)
			if similarity < minArtistSimilarity {
				logf(logger, "  reject %q / %q: artist similarity %.2f < %.2f (target artists=%v)", trackName, artistName, similarity, minArtistSimilarity, targetArtists)
				continue
			}
		}

		duration := mapFloatValue(result, "duration")
		hasDuration := targetDuration > 0 && duration > 0 && !math.IsNaN(duration) && !math.IsInf(duration, 0)
		diff := math.Inf(1)
		if hasDuration {
			diff = math.Abs(duration - float64(targetDuration))
		}
		logf(logger, "  candidate %q / %q: durationDiff=%.1fs (theirs=%.0fs, target=%ds)", trackName, artistName, diff, duration, targetDuration)
		if best == nil || (hasDuration && !bestHasDuration) || (hasDuration == bestHasDuration && diff < bestDiff) {
			bestDiff = diff
			bestHasDuration = hasDuration
			best = result
		}
	}
	return best
}

const singleLineIntroMaxStart = 1.0

// HasUsableLyrics reports whether a provider returned lyrics worth displaying.
// Some fuzzy karaoke endpoints return only one introductory/title line at
// time zero for tracks that have no lyrics. Treat that shape as empty without
// using language-specific heuristics.
func HasUsableLyrics(lines []Line) bool {
	if len(lines) == 0 {
		return false
	}
	if len(lines) == 1 && lines[0].Time >= 0 && lines[0].Time <= singleLineIntroMaxStart {
		return false
	}
	return true
}

// HasWordSyncedLyrics reports whether any line has word-level timing.
func HasWordSyncedLyrics(lines []Line) bool {
	for _, line := range lines {
		if len(line.Words) > 0 {
			return true
		}
	}
	return false
}

// ResultFromMap converts an LRCLIB-style JSON object into the common model.
func ResultFromMap(values map[string]interface{}, source string, quality int) *Result {
	if values == nil {
		return nil
	}
	synced := mapStringValue(values, "syncedLyrics", "synced")
	plain := mapStringValue(values, "plainLyrics", "plain")
	lines := ParseSyncedLyrics(synced)
	resultSource := source
	if lyricsfile := mapStringValue(values, "lyricsfile"); lyricsfile != "" {
		if fileLines, err := ParseLyricsfile(lyricsfile); err == nil && len(fileLines) > 0 {
			lines = fileLines
			resultSource = "lrclib-lyricsfile"
			if quality < 540 {
				quality = 540
			}
		}
	}
	if !HasUsableLyrics(lines) {
		return nil
	}
	if HasWordSyncedLyrics(lines) && quality < 500 {
		quality = 500
	}
	return &Result{
		Title:    mapStringValue(values, "trackName", "track", "title", "name", "musicName"),
		Artist:   mapStringValue(values, "artistName", "artist", "artists", "artistNames"),
		Album:    mapStringValue(values, "albumName", "album", "albumNames"),
		Duration: mapFloatValue(values, "duration"),
		Lines:    lines,
		Synced:   strings.TrimSpace(synced),
		Plain:    strings.TrimSpace(plain),
		Source:   resultSource,
		Quality:  quality,
	}
}

// ResultFromFields constructs a result after a provider has already parsed lines.
func ResultFromFields(values map[string]interface{}, lines []Line, synced, plain, source string, quality int) *Result {
	if !HasUsableLyrics(lines) {
		return nil
	}
	if HasWordSyncedLyrics(lines) && quality < 500 {
		quality = 500
	}
	return &Result{
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

// ResultMatchScore returns the metadata score used for provider selection.
func ResultMatchScore(result *Result, targetTitle string, targetArtists []string, targetAlbum string) float64 {
	if result == nil {
		return -1
	}
	titleScore := 0.5
	if result.Title != "" {
		titleScore = TitleSimilarity(result.Title, targetTitle)
	}
	artistScore := 0.5
	if result.Artist != "" {
		artistScore = BestSimilarityAgainst(result.Artist, targetArtists)
	}
	albumScore := 0.0
	if targetAlbum != "" && result.Album != "" {
		albumScore = TitleSimilarity(result.Album, targetAlbum)
	}
	return titleScore*5 + artistScore*3 + albumScore
}

func resultDurationDifference(result *Result, targetDuration int) float64 {
	if result == nil || math.IsNaN(result.Duration) || math.IsInf(result.Duration, 0) || result.Duration <= 0 || targetDuration <= 0 {
		return math.Inf(1)
	}
	return math.Abs(result.Duration - float64(targetDuration))
}

// ResultMetadataMatches reports whether the metadata that a provider supplied
// is compatible with the requested track. Empty provider fields are allowed
// because some sources omit one or more metadata values.
func ResultMetadataMatches(result *Result, targetTitle string, targetArtists []string) bool {
	if result == nil {
		return false
	}
	if result.Title != "" && TitleSimilarity(result.Title, targetTitle) < minTitleSimilarity {
		return false
	}
	if result.Artist != "" && BestSimilarityAgainst(result.Artist, targetArtists) < minArtistSimilarity {
		return false
	}
	return true
}

// isUntrustedEnhancedSource identifies the provider whose fuzzy fallback
// searches can return a word-synced track with echoed query metadata.
func isUntrustedEnhancedSource(source string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "synclrc-")
}

// IsDeferredResult reports whether a result should wait until the other
// providers have completed before being displayed. SyncLRC's fuzzy fallback
// can return a word-synced track with echoed query metadata, so displaying it
// immediately can cause a brief but visible wrong-lyrics flash.
func IsDeferredResult(result *Result) bool {
	return result != nil && isUntrustedEnhancedSource(result.Source)
}

// BetterResult reports whether candidate should replace current.
func BetterResult(candidate, current *Result, targetDuration int, targetTitle string, targetArtists []string, targetAlbum string) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	candidateWordSynced := HasWordSyncedLyrics(candidate.Lines)
	currentWordSynced := HasWordSyncedLyrics(current.Lines)
	candidateMatch := ResultMatchScore(candidate, targetTitle, targetArtists, targetAlbum)
	currentMatch := ResultMatchScore(current, targetTitle, targetArtists, targetAlbum)
	if currentWordSynced && !candidateWordSynced {
		if isUntrustedEnhancedSource(current.Source) && candidateMatch > currentMatch {
			return true
		}
		return false
	}
	if candidateWordSynced && !currentWordSynced && ResultMetadataMatches(candidate, targetTitle, targetArtists) {
		if !isUntrustedEnhancedSource(candidate.Source) || candidateMatch >= currentMatch {
			return true
		}
		return false
	}
	if candidateMatch > currentMatch+1.2 {
		return true
	}
	if math.Abs(candidateMatch-currentMatch) > 1.2 {
		return false
	}
	if candidate.Quality != current.Quality {
		return candidate.Quality > current.Quality
	}
	candidateDuration := resultDurationDifference(candidate, targetDuration)
	currentDuration := resultDurationDifference(current, targetDuration)
	if candidateDuration < currentDuration-0.5 {
		return true
	}
	if math.Abs(candidateDuration-currentDuration) > 0.5 {
		return false
	}
	if len(candidate.Lines) != len(current.Lines) {
		return len(candidate.Lines) > len(current.Lines)
	}
	return CountWords(candidate.Lines) > CountWords(current.Lines)
}

// CountWords counts timed words in a lyric result.
func CountWords(lines []Line) int {
	count := 0
	for _, line := range lines {
		count += len(line.Words)
	}
	return count
}
