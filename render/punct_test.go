package render

import (
	"strings"
	"testing"
)

// TestTrimTarget pins the punctuation pair rule against the browser-measured
// table. With the linecmp corpus font (Droid Sans Fallback), Chromium only
// halves the quote-like marks: a fullwidth opening bracket that the face draws
// itself blocks the pair instead of being compressed, which is why "。”（" and
// "；（" are left alone while "：“" and "”；" are not. scripts/linecmp.sh punct
// is the oracle for these rows; this test keeps the rule from drifting.
func TestTrimTarget(t *testing.T) {
	cases := []struct {
		pair string
		want int // index of the trimmed rune, -1 for no compression
	}{
		{"：“", 1},
		{"：‘", 1},
		{"？：“", 2},
		{"”；", 0},
		{"”，", 0},
		{"”」", 0},
		{"”（", -1}, // opening bracket wins the pair but cannot trim
		{"：「", -1},
		{"；（", -1},
		{"。（", -1},
		{"。”", -1}, // closing quote is not an opening mark; the period cannot trim
		{"。」", -1},
		{"——”", -1}, // the dash is not a fullwidth punctuation class
		{"……", -1},
		{"““", 1}, // only the second opening mark trims
		{"””", 0}, // only the first closing mark trims
		{"天：“", 2},
		{"中，文", -1},
	}
	for _, tc := range cases {
		text := []rune(tc.pair)
		// Find the only pair in two-rune cases, or the intended pair in the
		// three-rune ones (the trimmed index is relative to the whole string).
		found := false
		for a := 0; a+1 < len(text); a++ {
			target, ok := trimTarget(text, a, a+1)
			if tc.want >= 0 && ok && target == tc.want {
				found = true
				break
			}
			if tc.want < 0 && ok {
				t.Fatalf("%q: unexpected compression at %d", tc.pair, a)
			}
		}
		if tc.want >= 0 && !found {
			t.Fatalf("%q: expected rune %d to be trimmed", tc.pair, tc.want)
		}
	}
}

// TestCompressionOffByDefault verifies the switch is really off in the zero
// value: the shaped advances keep the full em width.
func TestCompressionOffByDefault(t *testing.T) {
	em := cjkEm(t, 18)
	l := cjkParagraph(t, "天：“地", AlignLeft, 2000, 18)
	p := &posPainter{}
	l.Page(0).Draw(p)
	glyphs := firstLineGlyphs(p)
	if len(glyphs) < 3 {
		t.Fatalf("expected glyphs, got %d", len(glyphs))
	}
	if !near(glyphs[2].advance, em, 0.05) {
		t.Fatalf("default quote advance = %.3f, want a full em %.3f", glyphs[2].advance, em)
	}
}

// TestHangingPunctuationLinesStayTogether runs hanging punctuation over a
// punctuation-dense paragraph and checks the two invariants a line breaker must
// keep: every rune is placed exactly once, and no line overhangs by more than
// half an em.
func TestHangingPunctuationLinesStayTogether(t *testing.T) {
	text := strings.Repeat("中文排版测试，包含标点符号。这是第二句！还有对话：“走吧。”", 20)
	wantRunes := len([]rune(text))
	// Narrow widths exercise the case where a pulled pair would run past the
	// end of the following wrapper line, which must not duplicate runes.
	for _, width := range []float32{30, 40, 60, 120, 320} {
		l := cjkParagraphRules(t, text, AlignLeft, width, 18, TextRules{HangingPunctuation: true})
		next := 0
		lines := 0
		items := itemsOf(&l.chapters[0])
		for i := range items {
			it := &items[i]
			if it.kind != itemLine {
				continue
			}
			lines++
			if int(it.start) != next {
				t.Fatalf("width %v: line %d starts at rune %d, want %d", width, lines, it.start, next)
			}
			next = int(it.end)
			if over := it.width - it.avail; over > 0 {
				em := resolveFontSize(it.para.styleAt(int(it.end)-1), l.opts.Typography)
				if over > 0.5*em+0.01 {
					t.Fatalf("width %v: line %d overhangs %.2f, more than half an em", width, lines, over)
				}
			}
		}
		if next != wantRunes {
			t.Fatalf("width %v: hanging layout placed %d of %d runes", width, next, wantRunes)
		}
		if lines < 2 {
			t.Fatalf("width %v: expected several lines, got %d", width, lines)
		}
	}
}
