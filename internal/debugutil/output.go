// Package debugutil contains small helpers shared by manual debug commands.
package debugutil

import (
	"strconv"
	"strings"
)

// Preview returns a quoted, bounded UTF-8 preview of a response body.
func Preview(body []byte, limit int) string {
	if len(body) == 0 {
		return "(empty)"
	}
	if limit < 1 {
		limit = 1
	}
	truncated := len(body) > limit
	if truncated {
		body = body[:limit]
	}
	preview := strconv.Quote(strings.TrimSpace(strings.ToValidUTF8(string(body), "�")))
	if truncated {
		preview += "..."
	}
	return preview
}
