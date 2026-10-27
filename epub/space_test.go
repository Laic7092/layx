package epub

import (
	"strings"
	"testing"
	"unicode"
)

// collapseSpaceReference is the rune-by-rune implementation the fast path has to
// agree with, kept here as the definition of what collapsing means.
func collapseSpaceReference(s string) (out string, leading, trailing bool) {
	var b strings.Builder
	inSpace := false
	started := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if started {
				inSpace = true
			} else {
				leading = true
			}
			continue
		}
		if inSpace {
			b.WriteByte(' ')
			inSpace = false
		}
		b.WriteRune(r)
		started = true
	}
	return b.String(), leading, inSpace
}

// TestCollapseSpaceAgreesWithTheRewrite pins the fast path to the rewrite: a
// text node that is already collapsed comes back as a slice of itself, and every
// other node is rewritten exactly as before.
func TestCollapseSpaceAgreesWithTheRewrite(t *testing.T) {
	cases := []string{
		"",
		" ",
		"\n\n\n",
		"\t \n",
		"hello",
		" hello",
		"hello ",
		" hello ",
		"a b c",
		"a  b",
		"a\tb",
		"a\nb",
		"a \n b",
		"a b  ",
		"  a b  ",
		"\u3000\u3000这是第一段。",
		"这是第一段。\u3000",
		"a\u3000\u3000b",
		"a\u00a0b",
		"a\u2003b",
		"\u3000",
		"a\xffb",
		"\xff",
		"a\uFFfdb",
		"  \u3000a b\t\n",
	}
	for _, s := range cases {
		wantOut, wantLead, wantTrail := collapseSpaceReference(s)
		gotOut, gotLead, gotTrail := collapseSpace(s)
		if gotOut != wantOut || gotLead != wantLead || gotTrail != wantTrail {
			t.Errorf("collapseSpace(%q) = (%q, %v, %v), want (%q, %v, %v)",
				s, gotOut, gotLead, gotTrail, wantOut, wantLead, wantTrail)
		}
	}
}

// TestCollapseSpaceKeepsAnAlreadyCollapsedNode checks the case that motivated
// the fast path: the text of a plain paragraph is handed back in place, with no
// copy of its own.
func TestCollapseSpaceKeepsAnAlreadyCollapsedNode(t *testing.T) {
	s := "这是第一段，主角又一次踏上了修炼之路。"
	out, leading, trailing := collapseSpace(s)
	if leading || trailing {
		t.Fatalf("collapseSpace(%q) reported leading=%v trailing=%v", s, leading, trailing)
	}
	if out != s {
		t.Fatalf("collapseSpace(%q) = %q, want the input back", s, out)
	}
	// A slice shares the node's bytes; a rewrite allocates. Nothing else in the
	// closure does, so any allocation means the text was copied.
	if allocs := testing.AllocsPerRun(100, func() {
		if _, _, _ = collapseSpace(s); out == "" {
			t.Fatal("unreachable")
		}
	}); allocs != 0 {
		t.Errorf("collapseSpace(%q) allocated %v times per run, want 0", s, allocs)
	}
}

// TestCollapseSpaceTrimsBySlicing covers indentation: the leading white space of
// a paragraph is dropped by moving the slice, not by rewriting the paragraph.
func TestCollapseSpaceTrimsBySlicing(t *testing.T) {
	s := "\u3000\u3000这是第一段。"
	out, leading, trailing := collapseSpace(s)
	if out != "这是第一段。" || !leading || trailing {
		t.Fatalf("collapseSpace(%q) = (%q, %v, %v)", s, out, leading, trailing)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		if _, _, _ = collapseSpace(s); out == "" {
			t.Fatal("unreachable")
		}
	}); allocs != 0 {
		t.Errorf("trimming %q allocated %v times per run, want 0", s, allocs)
	}
}
