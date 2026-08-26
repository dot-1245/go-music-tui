package ttycolor

import "testing"

func TestParseMode(t *testing.T) {
	tests := []struct {
		input string
		want  Mode
	}{
		{input: "auto", want: ModeAuto},
		{input: " ALWAYS ", want: ModeAlways},
		{input: "Never", want: ModeNever},
	}

	for _, test := range tests {
		mode, err := ParseMode(test.input)
		if err != nil {
			t.Errorf("ParseMode(%q) returned error: %v", test.input, err)
		}
		if mode != test.want {
			t.Errorf("ParseMode(%q) = %q, want %q", test.input, mode, test.want)
		}
	}

	if _, err := ParseMode("invalid"); err == nil {
		t.Fatal("ParseMode(invalid) returned nil error")
	}
}

func TestEnabled(t *testing.T) {
	tests := []struct {
		name    string
		mode    Mode
		isTTY   bool
		term    string
		noColor bool
		want    bool
	}{
		{name: "auto tty", mode: ModeAuto, isTTY: true, term: "xterm-256color", want: true},
		{name: "auto non tty", mode: ModeAuto, isTTY: false, term: "xterm-256color", want: false},
		{name: "auto dumb", mode: ModeAuto, isTTY: true, term: "dumb", want: false},
		{name: "auto no color", mode: ModeAuto, isTTY: true, term: "xterm", noColor: true, want: false},
		{name: "always non tty", mode: ModeAlways, isTTY: false, term: "dumb", want: true},
		{name: "always no color", mode: ModeAlways, isTTY: true, term: "xterm", noColor: true, want: true},
		{name: "never tty", mode: ModeNever, isTTY: true, term: "xterm", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Enabled(test.mode, test.isTTY, test.term, test.noColor); got != test.want {
				t.Fatalf("Enabled(%q, %t, %q, %t) = %t, want %t", test.mode, test.isTTY, test.term, test.noColor, got, test.want)
			}
		})
	}
}

func TestNewTheme(t *testing.T) {
	colored := New(ModeAlways, nil)
	want := Theme{
		Primary: "\x1b[31m",
		Accent:  "\x1b[32m",
		SubText: "\x1b[33m",
		Gray:    "\x1b[90m",
		Bold:    "\x1b[1m",
		BoldOff: "\x1b[22m",
		Reset:   "\x1b[0m",
	}
	if colored != want {
		t.Fatalf("New(always, nil) = %#v, want %#v", colored, want)
	}
	if !colored.Enabled() {
		t.Fatal("colored theme reports disabled")
	}

	plain := New(ModeNever, nil)
	if plain != (Theme{}) {
		t.Fatalf("New(never, nil) = %#v, want an empty theme", plain)
	}
	if plain.Enabled() {
		t.Fatal("plain theme reports enabled")
	}
}
