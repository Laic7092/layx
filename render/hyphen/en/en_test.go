package en

import (
	"slices"
	"testing"
)

func TestBreaksHyphen(t *testing.T) {
	got := New().Breaks([]rune("hyphen"))
	if !slices.Contains(got, 2) {
		t.Fatalf("Breaks(hyphen) = %v, want a break after hy", got)
	}
}

func TestBreaksRejectsNonWords(t *testing.T) {
	h := New()
	for _, word := range []string{"", "a", "the", "中文", "don't"} {
		if got := h.Breaks([]rune(word)); len(got) != 0 {
			t.Fatalf("Breaks(%q) = %v, want none", word, got)
		}
	}
}

func TestBreaksAreOrderedAndBounded(t *testing.T) {
	words := []string{"hyphenation", "extraordinary", "typography", "representation"}
	h := New()
	for _, word := range words {
		runes := []rune(word)
		got := h.Breaks(runes)
		if len(got) == 0 {
			t.Fatalf("Breaks(%q) returned no positions", word)
		}
		for i, pos := range got {
			if pos < 2 || pos > len(runes)-3 {
				t.Fatalf("Breaks(%q)[%d] = %d outside English hyphenmins", word, i, pos)
			}
			if i > 0 && pos <= got[i-1] {
				t.Fatalf("Breaks(%q) not ascending: %v", word, got)
			}
		}
	}
}

func TestNameIsStable(t *testing.T) {
	if got := New().Name(); got != "en-us" {
		t.Fatalf("Name() = %q", got)
	}
}
