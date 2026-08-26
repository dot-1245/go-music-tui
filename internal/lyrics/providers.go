package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	lrclibBaseURL     = "https://lrclib.net"
	syncLRCURL        = "https://api.synclrc.dev/lyrics"
	amllSearchURL     = "https://api.amll.dev/v1/lyrics/search"
	amllGetURL        = "https://api.amll.dev/v1/lyrics/get"
	providerUserAgent = "go-music-tui/1.0 (https://github.com/dot-1245/go-music-tui)"
	mbUserAgent       = providerUserAgent
)

// FetchProvider executes one named lyric provider and retains HTTP exchanges
// for the manual debug command. A missing lyric result is not itself an error.
func (c *Client) FetchProvider(ctx context.Context, provider string, request Request) ProviderReport {
	if ctx == nil {
		ctx = context.Background()
	}
	report := ProviderReport{Provider: strings.ToLower(strings.TrimSpace(provider))}
	switch report.Provider {
	case "lrclib":
		report.Result = c.fetchLRCLIB(ctx, request, &report)
	case "synclrc":
		report.Result = c.fetchSyncLRC(ctx, request, &report)
	case "amll":
		report.Result = c.fetchAMLL(ctx, request, &report)
	default:
		report.Requests = append(report.Requests, RequestRecord{Err: fmt.Errorf("unknown lyric provider %q", provider)})
	}
	return report
}

// FetchAll runs all providers concurrently and returns reports in stable
// provider order. A shared parent deadline still bounds the whole operation,
// but one slow provider no longer starves the others.
func (c *Client) FetchAll(ctx context.Context, request Request) []ProviderReport {
	if ctx == nil {
		ctx = context.Background()
	}
	providers := []string{"lrclib", "synclrc", "amll"}
	reports := make([]ProviderReport, 0, len(providers))
	results := make([]ProviderReport, len(providers))
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(providers))
	for index, provider := range providers {
		go func(index int, provider string) {
			defer waitGroup.Done()
			results[index] = c.FetchProvider(ctx, provider, request)
		}(index, provider)
	}
	waitGroup.Wait()
	return append(reports, results...)
}

func (c *Client) FetchLRCLIB(ctx context.Context, title string, artists []string, album string, durationSec int) *Result {
	return c.FetchProvider(ctx, "lrclib", NewRequest(title, artists, "", album, durationSec)).Result
}

func (c *Client) FetchSyncLRC(ctx context.Context, title, rawArtist string, artists []string, album string, durationSec int) *Result {
	return c.FetchProvider(ctx, "synclrc", NewRequest(title, artists, rawArtist, album, durationSec)).Result
}

func (c *Client) FetchAMLL(ctx context.Context, title, rawArtist string, artists []string, album string, durationSec int) *Result {
	return c.FetchProvider(ctx, "amll", NewRequest(title, artists, rawArtist, album, durationSec)).Result
}

func (c *Client) requestJSON(ctx context.Context, requestURL string, headers map[string]string) (interface{}, RequestRecord, error) {
	return c.requestJSONWithPurpose(ctx, requestURL, headers, requestPurpose(requestURL))
}

func (c *Client) requestJSONWithPurpose(ctx context.Context, requestURL string, headers map[string]string, purpose string) (interface{}, RequestRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	record := RequestRecord{URL: requestURL, Purpose: purpose}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		record.Err = err
		return nil, record, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		record.Err = err
		return nil, record, err
	}
	defer resp.Body.Close()
	record.StatusCode = resp.StatusCode
	maxBytes := c.MaxResponseBytes
	if maxBytes == 0 {
		maxBytes = 4 << 20
	}
	if maxBytes > 0 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		if int64(len(body)) > maxBytes {
			record.Err = fmt.Errorf("response exceeds %d-byte limit", maxBytes)
			return nil, record, record.Err
		}
		if readErr != nil {
			record.Err = readErr
			return nil, record, readErr
		}
		if c.CaptureBody {
			record.Body = body
		}
		return c.decodeResponse(body, record)
	}
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		record.Err = readErr
		return nil, record, readErr
	}
	if c.CaptureBody {
		record.Body = body
	}
	return c.decodeResponse(body, record)
}

func (c *Client) decodeResponse(body []byte, record RequestRecord) (interface{}, RequestRecord, error) {
	if record.StatusCode < 200 || record.StatusCode >= 300 {
		record.Err = fmt.Errorf("unexpected HTTP status %d", record.StatusCode)
		return nil, record, record.Err
	}
	var payload interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		record.Err = err
		return nil, record, err
	}
	return payload, record, nil
}

func requestPurpose(requestURL string) string {
	switch {
	case strings.Contains(requestURL, "/api/get"), strings.Contains(requestURL, "/v1/lyrics/get"), strings.Contains(requestURL, "api.synclrc.dev/lyrics"):
		return "lyrics"
	case strings.Contains(requestURL, "/api/search"), strings.Contains(requestURL, "/v1/lyrics/search"), strings.Contains(requestURL, "/recording?"):
		return "search"
	case strings.Contains(requestURL, "/artist/"):
		return "metadata"
	default:
		return "unknown"
	}
}

func (c *Client) requestProviderJSONWithPurpose(ctx context.Context, requestURL, purpose string) (interface{}, RequestRecord, error) {
	return c.requestJSONWithPurpose(ctx, requestURL, map[string]string{"User-Agent": providerUserAgent}, purpose)
}

func (c *Client) requestLRCLIB(ctx context.Context, requestURL string) (interface{}, RequestRecord, error) {
	return c.requestJSON(ctx, requestURL, map[string]string{
		"User-Agent":    providerUserAgent,
		"Lrclib-Client": "go-music-tui/1.0",
	})
}

func (c *Client) requestProviderJSON(ctx context.Context, requestURL string) (interface{}, RequestRecord, error) {
	return c.requestJSON(ctx, requestURL, map[string]string{"User-Agent": providerUserAgent})
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

func (c *Client) searchLRCLIB(ctx context.Context, params url.Values, report *ProviderReport) []map[string]interface{} {
	requestURL := lrclibBaseURL + "/api/search?" + params.Encode()
	payload, record, err := c.requestJSONWithPurpose(ctx, requestURL, map[string]string{
		"User-Agent":    providerUserAgent,
		"Lrclib-Client": "go-music-tui/1.0",
	}, "search")
	report.Requests = append(report.Requests, record)
	if err != nil {
		logf(c.Logger, "  search HTTP error (%s): %v", params.Encode(), err)
		return nil
	}
	items, ok := payload.([]interface{})
	if !ok {
		logf(c.Logger, "  search response was not an array (%s)", params.Encode())
		return nil
	}
	results := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if values, ok := item.(map[string]interface{}); ok {
			results = append(results, values)
		}
	}
	return results
}

func (c *Client) getLyricsExact(ctx context.Context, title, artist, album string, durationSec int, report *ProviderReport) (map[string]interface{}, bool) {
	params := url.Values{
		"track_name":  {title},
		"artist_name": {artist},
		"album_name":  {album},
		"duration":    {strconv.Itoa(durationSec)},
	}
	requestURL := lrclibBaseURL + "/api/get?" + params.Encode()
	payload, record, err := c.requestJSONWithPurpose(ctx, requestURL, map[string]string{
		"User-Agent":    providerUserAgent,
		"Lrclib-Client": "go-music-tui/1.0",
	}, "lyrics")
	report.Requests = append(report.Requests, record)
	if err != nil {
		logf(c.Logger, "  exact-get HTTP error (title=%q artist=%q): %v", title, artist, err)
		return nil, false
	}
	values, ok := payload.(map[string]interface{})
	if !ok {
		return nil, false
	}
	synced := mapStringValue(values, "syncedLyrics", "synced")
	lyricsfile := mapStringValue(values, "lyricsfile")
	if synced == "" && (lyricsfile == "" || func() bool {
		lines, err := ParseLyricsfile(lyricsfile)
		return err != nil || len(lines) == 0
	}()) {
		logf(c.Logger, "  exact-get hit but has no syncedLyrics (title=%q artist=%q)", title, artist)
		return nil, false
	}
	return values, true
}

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

func mbQueryEscape(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
}

func (c *Client) mbResolve(ctx context.Context, title, artist string, report *ProviderReport) (string, []string, bool) {
	query := fmt.Sprintf(`recording:"%s" AND artist:"%s"`, mbQueryEscape(title), mbQueryEscape(artist))
	requestURL := "https://musicbrainz.org/ws/2/recording?query=" + url.QueryEscape(query) + "&fmt=json&limit=5"
	payload, record, err := c.requestJSON(ctx, requestURL, map[string]string{"User-Agent": mbUserAgent})
	report.Requests = append(report.Requests, record)
	if err != nil {
		logf(c.Logger, "  musicbrainz HTTP error (query=%q): %v", query, err)
		return "", nil, false
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", nil, false
	}
	var result mbSearchResp
	if err := json.Unmarshal(encoded, &result); err != nil || len(result.Recordings) == 0 {
		logf(c.Logger, "  musicbrainz: no recordings for query=%q", query)
		return "", nil, false
	}
	recording := result.Recordings[0]
	if recording.Title == "" || len(recording.ArtistCredit) == 0 {
		return "", nil, false
	}
	primary := recording.ArtistCredit[0].Artist
	artistNames := []string{primary.Name}
	if primary.ID == "" {
		return recording.Title, artistNames, true
	}
	select {
	case <-ctx.Done():
		return "", nil, false
	case <-time.After(1100 * time.Millisecond):
	}
	aliasURL := "https://musicbrainz.org/ws/2/artist/" + url.PathEscape(primary.ID) + "?inc=aliases&fmt=json"
	aliasPayload, aliasRecord, aliasErr := c.requestJSON(ctx, aliasURL, map[string]string{"User-Agent": mbUserAgent})
	report.Requests = append(report.Requests, aliasRecord)
	if aliasErr == nil {
		encoded, marshalErr := json.Marshal(aliasPayload)
		var detail mbArtistDetail
		if marshalErr == nil && json.Unmarshal(encoded, &detail) == nil {
			seen := map[string]bool{primary.Name: true}
			for _, alias := range detail.Aliases {
				if alias.Name == "" || seen[alias.Name] {
					continue
				}
				seen[alias.Name] = true
				artistNames = append(artistNames, alias.Name)
				if len(artistNames) >= 6 {
					break
				}
			}
		}
	}
	return recording.Title, artistNames, true
}

func (c *Client) fetchLRCLIB(ctx context.Context, request Request, report *ProviderReport) *Result {
	logf(c.Logger, "lrclib fetch start: title=%q artists=%v album=%q duration=%ds", request.Title, request.Artists, request.Album, request.DurationSec)
	if IsInstrumentalTitle(request.Title) {
		logf(c.Logger, "=> skipped: title matched instrumental pattern")
		return nil
	}
	cleanTitle := CleanTrackTitle(request.Title)
	if cleanTitle != request.Title {
		logf(c.Logger, "cleaned title: %q -> %q", request.Title, cleanTitle)
	}
	queryArtists := request.Artists
	if len(queryArtists) == 0 {
		logf(c.Logger, "no artist metadata from playerctl, falling back to empty artist")
		queryArtists = []string{""}
	}
	for _, artist := range queryArtists {
		if exact, ok := c.getLyricsExact(ctx, cleanTitle, artist, request.Album, request.DurationSec, report); ok {
			return ResultFromMap(exact, "lrclib-exact", 390)
		}
		if cleanTitle != request.Title {
			if exact, ok := c.getLyricsExact(ctx, request.Title, artist, request.Album, request.DurationSec, report); ok {
				return ResultFromMap(exact, "lrclib-exact", 390)
			}
		}
	}

	var allResults []map[string]interface{}
	for _, artist := range queryArtists {
		results := c.searchLRCLIB(ctx, url.Values{"track_name": {cleanTitle}, "artist_name": {artist}}, report)
		logf(c.Logger, "stage1 search track_name=%q artist_name=%q -> %d results", cleanTitle, artist, len(results))
		allResults = append(allResults, results...)
	}
	if request.Album != "" {
		results := c.searchLRCLIB(ctx, url.Values{"track_name": {cleanTitle}, "album_name": {request.Album}}, report)
		logf(c.Logger, "stage2 search track_name=%q album_name=%q -> %d results", cleanTitle, request.Album, len(results))
		allResults = append(allResults, results...)
	}
	results := c.searchLRCLIB(ctx, url.Values{"track_name": {cleanTitle}}, report)
	logf(c.Logger, "stage3 search track_name=%q -> %d results", cleanTitle, len(results))
	allResults = append(allResults, results...)
	if cleanTitle != request.Title {
		results := c.searchLRCLIB(ctx, url.Values{"track_name": {request.Title}}, report)
		logf(c.Logger, "stage4 search track_name=%q -> %d results", request.Title, len(results))
		allResults = append(allResults, results...)
	}

	targetArtists := append([]string{}, request.Artists...)
	for _, artist := range queryArtists {
		if artist == "" {
			continue
		}
		mbTitle, mbArtists, ok := c.mbResolve(ctx, cleanTitle, artist, report)
		if !ok {
			continue
		}
		logf(c.Logger, "musicbrainz resolved artist=%q -> title=%q aliases=%v", artist, mbTitle, mbArtists)
		targetArtists = append(targetArtists, mbArtists...)
		for _, alias := range mbArtists {
			results := c.searchLRCLIB(ctx, url.Values{"track_name": {mbTitle}, "artist_name": {alias}}, report)
			logf(c.Logger, "stage5 search track_name=%q artist_name=%q -> %d results", mbTitle, alias, len(results))
			allResults = append(allResults, results...)
		}
		break
	}
	logf(c.Logger, "total raw results collected: %d", len(allResults))
	if len(allResults) == 0 {
		logf(c.Logger, "=> giving up: no results from any search stage")
		return nil
	}
	best := PickBestMatch(allResults, request.DurationSec, cleanTitle, targetArtists, c.Logger)
	if best == nil {
		logf(c.Logger, "=> giving up: every candidate was rejected by the similarity filters")
		return nil
	}
	logf(c.Logger, "=> selected: lrclib id=%v track=%v artist=%v album=%v", best["id"], best["trackName"], best["artistName"], best["albumName"])
	return ResultFromMap(best, "lrclib-search", 390)
}

func syncLRCArtistPlan(rawArtist string, artists []string) []string {
	plan := appendUniqueString(nil, rawArtist)
	for _, artist := range artists {
		plan = appendUniqueString(plan, artist)
	}
	return plan
}

// ParseSyncLRCResult converts a SyncLRC response into the common model.
func ParseSyncLRCResult(payload interface{}) *Result {
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
		lines := ParseSyncedLyrics(karaoke)
		if len(lines) > 0 {
			if HasWordSyncedLyrics(lines) {
				return ResultFromFields(values, lines, karaoke, plain, "synclrc-enhanced", 600)
			}
			synced = karaoke
		}
	}
	if synced != "" {
		lines := ParseSyncedLyrics(synced)
		if len(lines) > 0 {
			return ResultFromFields(values, lines, synced, plain, "synclrc-synced", 300)
		}
	}
	return nil
}

func (c *Client) fetchSyncLRC(ctx context.Context, request Request, report *ProviderReport) *Result {
	for _, artist := range syncLRCArtistPlan(request.RawArtist, request.Artists) {
		params := url.Values{"track": {request.Title}, "artist": {artist}, "album": {request.Album}, "type": {"karaoke"}}
		if request.DurationSec > 0 {
			params.Set("duration", strconv.Itoa(request.DurationSec))
		}
		payload, record, err := c.requestProviderJSONWithPurpose(ctx, syncLRCURL+"?"+params.Encode(), "lyrics")
		report.Requests = append(report.Requests, record)
		if err != nil {
			logf(c.Logger, "synclrc request failed (title=%q artist=%q): %v", request.Title, artist, err)
			continue
		}
		if result := ParseSyncLRCResult(payload); result != nil {
			return result
		}
	}
	return nil
}

// AMLLCandidateScore returns the metadata score used before fetching TTML.
func AMLLCandidateScore(candidate map[string]interface{}, title string, artists []string, album string) (float64, bool) {
	candidateTitle := mapStringValue(candidate, "musicName", "musicNames", "trackName", "title", "name")
	candidateArtist := mapStringValue(candidate, "artistName", "artist", "artistNames", "artists")
	if candidateTitle != "" && TitleSimilarity(candidateTitle, title) < 0.45 {
		return 0, false
	}
	if candidateArtist != "" && BestSimilarityAgainst(candidateArtist, artists) < 0.4 {
		return 0, false
	}
	titleScore := 0.5
	if candidateTitle != "" {
		titleScore = TitleSimilarity(candidateTitle, title)
	}
	artistScore := 0.5
	if candidateArtist != "" {
		artistScore = BestSimilarityAgainst(candidateArtist, artists)
	}
	albumScore := 0.0
	candidateAlbum := mapStringValue(candidate, "albumName", "album", "albumNames")
	if album != "" && candidateAlbum != "" {
		albumScore = TitleSimilarity(candidateAlbum, album)
	}
	return titleScore*5 + artistScore*3 + albumScore, true
}

// SelectAMLLCandidate selects the highest-scoring AMLL search result.
func SelectAMLLCandidate(candidates []map[string]interface{}, title string, artists []string, album string) map[string]interface{} {
	var best map[string]interface{}
	bestScore := -1.0
	for _, candidate := range candidates {
		score, ok := AMLLCandidateScore(candidate, title, artists, album)
		if ok && score > bestScore {
			best = candidate
			bestScore = score
		}
	}
	return best
}

func amllArtistPlan(rawArtist string, artists []string) []string {
	plan := appendUniqueString(nil, rawArtist)
	for _, artist := range artists {
		plan = appendUniqueString(plan, artist)
	}
	return plan
}

func amllSearchPlan(title, rawArtist string, artists []string, album string) []string {
	titles := []string{CleanTrackTitle(title)}
	if titles[0] != title {
		titles = append(titles, title)
	}
	artistPlan := amllArtistPlan(rawArtist, artists)
	var plan []string
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
				appendPlan(url.Values{"musicName": {candidateTitle}, "artistName": {artist}, "albumName": {album}})
			}
			appendPlan(url.Values{"musicName": {candidateTitle}, "artistName": {artist}})
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

func (c *Client) fetchAMLL(ctx context.Context, request Request, report *ProviderReport) *Result {
	for _, searchURL := range amllSearchPlan(request.Title, request.RawArtist, request.Artists, request.Album) {
		payload, record, err := c.requestProviderJSONWithPurpose(ctx, searchURL, "search")
		report.Requests = append(report.Requests, record)
		if err != nil {
			logf(c.Logger, "amll search failed (%s): %v", searchURL, err)
			continue
		}
		candidateArtists := append([]string(nil), request.Artists...)
		candidateArtists = appendUniqueString(candidateArtists, request.RawArtist)
		candidate := SelectAMLLCandidate(responseItems(payload), request.Title, candidateArtists, request.Album)
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
		getPayload, getRecord, err := c.requestProviderJSONWithPurpose(ctx, getURL, "lyrics")
		report.Requests = append(report.Requests, getRecord)
		if err != nil {
			logf(c.Logger, "amll get failed (%s): %v", getURL, err)
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
		lines, err := ParseTTMLLyrics(ttml)
		if err != nil || len(lines) == 0 {
			logf(c.Logger, "amll TTML parse failed (title=%q): %v", request.Title, err)
			continue
		}
		metadata := mergeAMLLMetadata(candidate, data)
		plain := make([]string, 0, len(lines))
		for _, line := range lines {
			plain = append(plain, line.Text)
		}
		quality := 450
		source := "amll-ttml"
		if HasWordSyncedLyrics(lines) {
			quality = 650
			source = "amll-ttml-word"
		}
		return ResultFromFields(metadata, lines, "", strings.Join(plain, "\n"), source, quality)
	}
	return nil
}
