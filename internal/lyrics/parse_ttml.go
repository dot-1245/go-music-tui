package lyrics

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
)

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
			appendTTMLText(&builder, normalizeTTMLText(child.Text))
			continue
		}
		role := ttmlRole(child.Node)
		if role == "x-translation" || role == "x-roman" {
			continue
		}
		appendTTMLText(&builder, ttmlMainText(child.Node))
	}
	return builder.String()
}

func appendTTMLText(builder *strings.Builder, text string) {
	if text == "" {
		return
	}
	if builder.Len() > 0 {
		current := []rune(builder.String())
		next := []rune(text)
		if len(current) > 0 && len(next) > 0 && needsTTMLSpace(current[len(current)-1], next[0]) {
			builder.WriteByte(' ')
		}
	}
	builder.WriteString(text)
}

func needsTTMLSpace(previous, next rune) bool {
	if unicode.IsSpace(previous) || unicode.IsSpace(next) {
		return false
	}
	if isWideTTMLRune(previous) || isWideTTMLRune(next) {
		return false
	}
	return (unicode.IsLetter(previous) || unicode.IsDigit(previous)) &&
		(unicode.IsLetter(next) || unicode.IsDigit(next))
}

func isWideTTMLRune(value rune) bool {
	return (value >= 0x1100 && value <= 0x115f) ||
		(value >= 0x2e80 && value <= 0xa4cf) ||
		(value >= 0xac00 && value <= 0xd7a3) ||
		(value >= 0xf900 && value <= 0xfaff) ||
		(value >= 0xff00 && value <= 0xff60) ||
		(value >= 0x1f300 && value <= 0x1faff)
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

func collectTTMLMainContent(node *ttmlNode) ([]string, []Word) {
	var parts []string
	var words []Word
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
			word := Word{Time: begin, EndTime: math.NaN(), Text: text}
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

func ParseTTMLLyrics(source string) ([]Line, error) {
	document, err := parseTTMLDocument(source)
	if err != nil {
		return nil, err
	}
	paragraphs := make([]*ttmlNode, 0)
	collectTTMLParagraphs(document, &paragraphs)
	lines := make([]Line, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		start, ok := parseTTMLTime(ttmlAttribute(paragraph, "begin"))
		if !ok {
			continue
		}
		end, hasEnd := parseTTMLTime(ttmlAttribute(paragraph, "end"))
		parts, words := collectTTMLMainContent(paragraph)
		var textBuilder strings.Builder
		for _, part := range parts {
			appendTTMLText(&textBuilder, part)
		}
		text := strings.TrimSpace(textBuilder.String())
		if text == "" && len(words) > 0 {
			var builder strings.Builder
			for _, word := range words {
				appendTTMLText(&builder, word.Text)
			}
			text = strings.TrimSpace(builder.String())
		}
		if text == "" {
			continue
		}

		line := Line{Time: start, EndTime: math.NaN(), Text: text, Words: words}
		if hasEnd {
			line.EndTime = end
		}
		lines = append(lines, line)
	}
	return normalizeLyricLines(lines), nil
}
