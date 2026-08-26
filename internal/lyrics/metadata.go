package lyrics

import (
	"strings"
)

func comparableLyricMetadata(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("“", "", "”", "", "\"", "", "'", "",
		"-", " ", "–", " ", "—", " ", ":", " ", "|", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "【", " ", "】", " ").Replace(value)
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
	appendCandidate(CleanTrackTitle(title))
	for _, artist := range artists {
		artist = strings.TrimSpace(artist)
		appendCandidate(artist)
		appendCandidate(title + " - " + artist)
		appendCandidate(artist + " - " + title)
	}
	return values
}

func leadingLyricMetadataWordCount(words []Word, candidates []string) int {
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

func metadataSuffixWordCount(words []Word, start int) int {
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

// StripLeadingLyricMetadata removes timed title/credit tokens occasionally
// returned before the actual lyrics by karaoke providers. It only removes an
// exact leading title/artist match, so a real lyric line containing the title
// later in the text is preserved.
func StripLeadingLyricMetadata(lines []Line, title string, artists []string) []Line {
	if len(lines) == 0 {
		return lines
	}
	// Provider results may be cached or shared with diagnostics. Work on a
	// deep copy so stripping display-only metadata never mutates the caller's
	// parsed representation.
	lines = cloneLyricLines(lines)
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
				line.Words = append([]Word(nil), line.Words[matched:]...)
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

func cloneLyricLines(lines []Line) []Line {
	clone := make([]Line, len(lines))
	for index, line := range lines {
		clone[index] = line
		clone[index].Words = append([]Word(nil), line.Words...)
	}
	return clone
}
