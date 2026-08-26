// Package ttycolor provides the terminal-native color palette used by the TUI.
package ttycolor

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// Mode controls when ANSI colors are emitted.
type Mode string

const (
	// ModeAuto enables colors only for a usable terminal environment.
	ModeAuto Mode = "auto"
	// ModeAlways enables colors regardless of the output destination.
	ModeAlways Mode = "always"
	// ModeNever disables ANSI colors.
	ModeNever Mode = "never"
)

// ParseMode validates a color mode supplied by a command-line flag or another
// user-facing configuration source.
func ParseMode(value string) (Mode, error) {
	mode := Mode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case ModeAuto, ModeAlways, ModeNever:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported color mode %q (want auto, always, or never)", value)
	}
}

// IsTTY reports whether file points to a terminal understood by x/term.
func IsTTY(file *os.File) bool {
	if file == nil {
		return false
	}
	fd := int(file.Fd())
	return term.IsTerminal(fd)
}

// Enabled determines whether ANSI colors should be emitted. The inputs are
// explicit so the decision can be tested without requiring an actual TTY.
// NO_COLOR is represented by noColor and applies to auto mode; an explicit
// always mode remains an override for users who need it.
func Enabled(mode Mode, isTTY bool, termName string, noColor bool) bool {
	switch mode {
	case ModeAlways:
		return true
	case ModeNever:
		return false
	case ModeAuto:
		return isTTY && !strings.EqualFold(strings.TrimSpace(termName), "dumb") && !noColor
	default:
		return false
	}
}

// Theme contains semantic foreground styles used by the main UI and the
// karaoke renderer. Empty fields intentionally mean "emit no ANSI code".
type Theme struct {
	Primary, Accent, SubText, Gray string
	Bold, BoldOff, Reset           string
}

// New returns the terminal-native ANSI palette for the current environment.
// ANSI 16-color codes are resolved by the terminal, so the user's kitty,
// foot, Alacritty, tmux, or other terminal palette supplies the actual RGB
// values without requiring a separate theme file or external tool.
func New(mode Mode, output *os.File) Theme {
	if !Enabled(mode, IsTTY(output), os.Getenv("TERM"), os.Getenv("NO_COLOR") != "") {
		return Theme{}
	}

	return Theme{
		// Use the normal ANSI slots intentionally. Generated Kitty palettes may
		// assign the bright slots (91-97) to dark container colors.
		Primary: "\x1b[31m", // terminal color1 / primary
		Accent:  "\x1b[32m", // terminal color2 / secondary
		SubText: "\x1b[33m", // terminal color3 / tertiary
		Gray:    "\x1b[90m", // terminal color8 / subdued secondary text
		Bold:    "\x1b[1m",
		BoldOff: "\x1b[22m",
		Reset:   "\x1b[0m",
	}
}

// Enabled reports whether this theme emits ANSI color sequences.
func (t Theme) Enabled() bool {
	return t.Reset != ""
}
