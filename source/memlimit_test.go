package main

import (
	"math"
	"testing"
)

func TestGoMemLimit(t *testing.T) {
	const gb = 1 << 30
	if got := goMemLimit(0, 90); got != math.MaxInt64 {
		t.Errorf("unknown memory: %d, want no limit", got)
	}
	if got, want := goMemLimit(3*gb, 0), int64(3*gb/100*70); got != want {
		t.Errorf("default cap: %d, want %d", got, want)
	}
	if got, want := goMemLimit(3*gb, 40), int64(3*gb/100*60); got != want {
		t.Errorf("40%% cap: %d, want %d", got, want)
	}
	if got, want := goMemLimit(3*gb, 100), int64(3*gb/100*92); got != want {
		t.Errorf("100%% cap is held at 92%%: %d, want %d", got, want)
	}
}
