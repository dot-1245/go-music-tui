package main

import "testing"

func TestIsPlayerCycleKey(t *testing.T) {
	if !isPlayerCycleKey('F') || !isPlayerCycleKey('f') {
		t.Fatal("F/f was not recognized as the player cycle key")
	}
	if isPlayerCycleKey('R') || isPlayerCycleKey('e') {
		t.Fatal("unrelated key was recognized as the player cycle key")
	}
}
