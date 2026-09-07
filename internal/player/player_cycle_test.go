package player

import "testing"

func TestNextPlayerCyclesDistinctNames(t *testing.T) {
	players := []string{"Feishin", "", "firefox", "Feishin"}
	tests := []struct {
		current string
		want    string
	}{
		{current: "Feishin", want: "firefox"},
		{current: "firefox", want: "Feishin"},
		{current: "missing", want: "Feishin"},
	}
	for _, test := range tests {
		if got := NextPlayer(players, test.current); got != test.want {
			t.Errorf("NextPlayer(%v, %q) = %q; want %q", players, test.current, got, test.want)
		}
	}
	if got := NextPlayer(nil, "Feishin"); got != "" {
		t.Fatalf("NextPlayer(nil, ...) = %q; want empty", got)
	}
}
