package lyrics

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	lyricLineTagRe = regexp.MustCompile(`\[(\d+):(\d{2})(?:[.:](\d+))?\]`)
	lyricWordTagRe = regexp.MustCompile(`<(\d+):(\d{2})(?:[.:](\d+))?>`)
)

type Word struct {
	Time    float64
	EndTime float64
	Text    string
}

type Line struct {
	Time    float64
	EndTime float64
	Text    string
	Words   []Word
}

func parseLrcTimestamp(minutes, seconds, fraction string) (float64, bool) {
	minuteValue, err := strconv.Atoi(minutes)
	if err != nil {
		return 0, false
	}
	secondValue, err := strconv.Atoi(seconds)
	if err != nil || secondValue < 0 || secondValue >= 60 {
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
func parseInlineWordTags(row string) (string, []Word) {
	withoutLineTags := lyricLineTagRe.ReplaceAllString(row, "")
	matches := lyricWordTagRe.FindAllStringSubmatchIndex(withoutLineTags, -1)
	words := make([]Word, 0, len(matches))
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
				words = append(words, Word{
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
			words = append(words, Word{
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

func normalizeLyricLines(lines []Line) []Line {
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

// ParseSyncedLyrics accepts ordinary LRC and enhanced LRC. An enhanced row
// keeps both the complete line text and its word-level timing information.
func ParseSyncedLyrics(synced string) []Line {
	parsed := make([]Line, 0)
	for _, row := range strings.Split(strings.ReplaceAll(synced, "\r\n", "\n"), "\n") {
		text, words := parseInlineWordTags(row)
		matches := lyricLineTagRe.FindAllStringSubmatchIndex(row, -1)
		if len(matches) == 0 && len(words) > 0 {
			parsed = append(parsed, Line{
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
			lineWords := append([]Word(nil), words...)
			parsed = append(parsed, Line{
				Time:    time,
				EndTime: math.NaN(),
				Text:    text,
				Words:   lineWords,
			})
		}
	}
	return normalizeLyricLines(parsed)
}
