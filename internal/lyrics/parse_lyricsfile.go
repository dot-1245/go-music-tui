package lyrics

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

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

// ParseLyricsfile handles LRCLIB's YAML Lyricsfile format. It is kept as a
// separate parser because Lyricsfile carries explicit word start/end times,
// unlike ordinary LRC. The YAML dependency is intentionally limited to this
// stable boundary; the rest of the application still uses the common model.
func ParseLyricsfile(source string) ([]Line, error) {
	if strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("empty Lyricsfile")
	}
	var document lyricsFileDocument
	if err := yaml.Unmarshal([]byte(source), &document); err != nil {
		return nil, err
	}

	lines := make([]Line, 0, len(document.Lines))
	for _, rawLine := range document.Lines {
		startMS, ok := yamlNumber(rawLine.StartMS)
		if !ok {
			continue
		}
		words := make([]Word, 0, len(rawLine.Words))
		for _, rawWord := range rawLine.Words {
			wordStartMS, ok := yamlNumber(rawWord.StartMS)
			if !ok {
				continue
			}
			word := Word{
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

		line := Line{
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
