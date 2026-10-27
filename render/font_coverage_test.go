package render

import (
	"bytes"
	"sync"
	"testing"

	"github.com/laix/layx/render/paint"
)

// TestFontSetCoverage is the §3.5 coverage contract: the queried set (DejaVu
// Serif, loaded under the family "Test Serif" by the shared test helpers) covers
// Latin, Greek and Cyrillic, but not Arabic, Hebrew, Thai, Devanagari or CJK.
func TestFontSetCoverage(t *testing.T) {
	fs := testFontSet(t)

	covered := []rune{'a', 'Z', '0', ' ', '-', 'é', '\u03B1', '\u0430', '\u2014'}
	for _, r := range covered {
		if !fs.Coverage(r) {
			t.Errorf("Coverage(U+%04X) = false, want true", r)
		}
	}
	uncovered := []rune{
		'\u0627', // Arabic alef
		'\u0644', // Arabic lam
		'\u05D0', // Hebrew alef
		'\u0E01', // Thai ko kai
		'\u0915', // Devanagari ka
		'\u4E16', // CJK 世
		'\U0001F600',
	}
	for _, r := range uncovered {
		if fs.Coverage(r) {
			t.Errorf("Coverage(U+%04X) = true, want false", r)
		}
	}

	// Invalid rune values are "not covered", not a panic.
	for _, r := range []rune{-1, unicodeMaxRune + 1, 0x7FFFFFFF} {
		if fs.Coverage(r) {
			t.Errorf("Coverage(U+%X) = true, want false for an invalid rune", uint32(r))
		}
	}
}

// unicodeMaxRune mirrors unicode.MaxRune so the table above can include an
// out-of-range value without importing the package just for one constant.
const unicodeMaxRune = 0x10FFFF

// TestFontSetHasFamily checks that families are the names the host declared in
// FontFace (that is what layout resolves against), compared case- and
// space-insensitively.
func TestFontSetHasFamily(t *testing.T) {
	fs := testFontSet(t)
	for _, family := range []string{"Test Serif", "test serif", "  Test Serif  ", "TEST SERIF"} {
		if !fs.HasFamily(family) {
			t.Errorf("HasFamily(%q) = false, want true", family)
		}
	}
	for _, family := range []string{"", "   ", "DejaVu Serif", "unknown"} {
		if fs.HasFamily(family) {
			t.Errorf("HasFamily(%q) = true, want false", family)
		}
	}

	data := loadTestFont(t).Data

	// A host that declares the file's own name gets that name back.
	named, err := NewFontSet(FontFace{Family: "DejaVu Serif", Data: data})
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}
	if !named.HasFamily("DejaVu Serif") {
		t.Error(`HasFamily("DejaVu Serif") = false, want true`)
	}
	if named.HasFamily("Test Serif") {
		t.Error(`HasFamily("Test Serif") = true on a set that only declared "DejaVu Serif"`)
	}

	// An empty FontFace.Family is stored as "default", the layout's fallback.
	defaulted, err := NewFontSet(FontFace{Data: data})
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}
	if !defaulted.HasFamily("default") {
		t.Error(`HasFamily("default") = false, want true`)
	}
}

// TestFontSetResolve checks that Resolve reports the face layout would pick and
// returns false for runes no face covers.
func TestFontSetResolve(t *testing.T) {
	fs := testFontSet(t)
	st := Style{FontFamily: "Test Serif", FontWeight: 400}

	id, ok := fs.Resolve(st, 'a')
	if !ok || id != 0 {
		t.Fatalf("Resolve('a') = (%d, %v), want (0, true)", id, ok)
	}
	if _, ok := fs.Resolve(st, '\u0627'); ok {
		t.Fatal("Resolve(Arabic alef) = true, want false: no face covers it")
	}
	if id, ok := fs.Resolve(st, -1); ok || id != InvalidFace {
		t.Fatalf("Resolve(-1) = (%d, %v), want (InvalidFace, false)", id, ok)
	}

	data := loadTestFont(t).Data

	// Family preference: the requested family wins even when it is not first.
	byFamily, err := NewFontSet(
		FontFace{Family: "Fallback Serif", Weight: 400, Data: data},
		FontFace{Family: "Test Serif", Weight: 400, Data: data},
	)
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}
	if id, ok := byFamily.Resolve(Style{FontFamily: "Test Serif"}, 'a'); !ok || id != 1 {
		t.Fatalf("Resolve with matching family = (%d, %v), want (1, true)", id, ok)
	}
	if id, ok := byFamily.Resolve(Style{FontFamily: "absent"}, 'a'); !ok || id != 0 {
		t.Fatalf("Resolve with absent family = (%d, %v), want (0, true) fallback", id, ok)
	}

	// Slant preference.
	bySlant, err := NewFontSet(
		FontFace{Family: "Same", Slant: SlantNone, Data: data},
		FontFace{Family: "Same", Slant: SlantItalic, Data: data},
	)
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}
	if id, ok := bySlant.Resolve(Style{FontFamily: "Same", FontSlant: SlantItalic}, 'a'); !ok || id != 1 {
		t.Fatalf("Resolve italic = (%d, %v), want (1, true)", id, ok)
	}
	if id, ok := bySlant.Resolve(Style{FontFamily: "Same"}, 'a'); !ok || id != 0 {
		t.Fatalf("Resolve upright = (%d, %v), want (0, true)", id, ok)
	}

	// Weight preference.
	byWeight, err := NewFontSet(
		FontFace{Family: "Same", Weight: 400, Data: data},
		FontFace{Family: "Same", Weight: 700, Data: data},
	)
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}
	if id, ok := byWeight.Resolve(Style{FontFamily: "Same", FontWeight: 700}, 'a'); !ok || id != 1 {
		t.Fatalf("Resolve bold = (%d, %v), want (1, true)", id, ok)
	}
	if id, ok := byWeight.Resolve(Style{FontFamily: "Same", FontWeight: 400}, 'a'); !ok || id != 0 {
		t.Fatalf("Resolve regular = (%d, %v), want (0, true)", id, ok)
	}
}

// TestFontSetResolveMatchesOrder pins Resolve to the same preference order the
// layout's fallback resolution uses: the first face in order that covers the
// rune.
func TestFontSetResolveMatchesOrder(t *testing.T) {
	data := loadTestFont(t).Data
	fs, err := NewFontSet(
		FontFace{Family: "Serif A", Weight: 400, Data: data},
		FontFace{Family: "Serif B", Weight: 700, Slant: SlantItalic, Data: data},
		FontFace{Family: "Serif A", Weight: 700, Data: data},
		FontFace{Family: "Serif C", Weight: 300, Slant: SlantItalic, Data: data},
	)
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}
	styles := []Style{
		{FontFamily: "Serif A"},
		{FontFamily: "Serif B", FontWeight: 700},
		{FontFamily: "Serif A", FontWeight: 700, FontSlant: SlantItalic},
		{FontFamily: "serif c", FontWeight: 300, FontSlant: SlantItalic},
		{FontFamily: "unknown", FontWeight: 400},
	}
	runes := []rune{'a', 'Z', '\u03B1', '\u0627', '\u4E16'}
	for _, st := range styles {
		for _, r := range runes {
			want := InvalidFace
			for _, i := range fs.order(st.FontFamily, st.FontWeight, st.FontSlant) {
				if _, ok := fs.entries[i].font.NominalGlyph(r); ok {
					want = FaceID(i)
					break
				}
			}
			got, ok := fs.Resolve(st, r)
			if ok != (want != InvalidFace) || got != want {
				t.Fatalf("Resolve(%q, %U) = (%d, %v), want (%d, %v)",
					st.FontFamily, r, got, ok, want, want != InvalidFace)
			}
		}
	}
}

// TestFontSetFace checks the host convenience accessor: it returns the bytes and
// family of the face, and false for ids the set did not produce.
func TestFontSetFace(t *testing.T) {
	face := loadTestFont(t)
	fs, err := NewFontSet(face)
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}
	got, ok := fs.Face(0)
	if !ok {
		t.Fatal("Face(0) = false, want true")
	}
	if got.Family != "Test Serif" {
		t.Fatalf("Face(0).Family = %q, want %q", got.Family, "Test Serif")
	}
	if got.Weight != 400 {
		t.Fatalf("Face(0).Weight = %d, want 400", got.Weight)
	}
	if len(got.Data) != len(face.Data) {
		t.Fatalf("Face(0).Data has %d bytes, want %d", len(got.Data), len(face.Data))
	}
	if !bytes.Equal(got.Data, face.Data) {
		t.Fatal("Face(0).Data differs from the FontFace data")
	}

	for _, id := range []FaceID{InvalidFace, -1, 1, 99} {
		if _, ok := fs.Face(id); ok {
			t.Fatalf("Face(%d) = true, want false", id)
		}
	}
}

// TestFontSetQueriesConcurrent exercises the read-only coverage queries from
// several goroutines; run with -race it proves they share immutable state only.
func TestFontSetQueriesConcurrent(t *testing.T) {
	fs := testFontSet(t)
	st := Style{FontFamily: "Test Serif"}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				fs.Coverage('a')
				fs.Coverage('\u0627')
				fs.HasFamily("Test Serif")
				fs.HasFamily("nope")
				fs.Resolve(st, 'a')
				fs.Resolve(st, '\u4E16')
				fs.Face(0)
				fs.Face(InvalidFace)
			}
		}()
	}
	wg.Wait()
}

// Compile-time check that the host-facing accessor returns the paint package's
// font handle type.
var _ func(FaceID) (paint.Font, bool) = (*FontSet)(nil).Face
