package render

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/laix/layx/render/paint"
)

// FontFace describes one font file (or one face in a collection).
type FontFace struct {
	Family string
	// Weight is a CSS-style numeric weight (100..900). Zero means 400.
	Weight int
	Slant  Slant
	// Data is the complete font file. NewFontSet keeps the slice instead of
	// copying it: a 16 MB CJK collection would otherwise be duplicated for the
	// lifetime of the set, and the library only ever reads it. The caller must
	// not modify or reuse the buffer while the FontSet is alive (the same
	// contract paint.Font.Data documents).
	Data []byte
	// Index selects a face inside a TrueType/OpenType collection.
	Index int
}

type fontEntry struct {
	ref   *paint.Font
	font  *font.Font
	index int
}

// FontSet is an immutable collection of font faces with fallback resolution.
//
// A FontSet is safe for concurrent use. Internally each layout worker creates
// its own font.Face caches because go-text faces are not concurrency-safe.
type FontSet struct {
	entries []fontEntry
}

// NewFontSet parses faces and returns an immutable font set.
func NewFontSet(faces ...FontFace) (*FontSet, error) {
	if len(faces) == 0 {
		return nil, fmt.Errorf("%w: NewFontSet called without faces", ErrNoFonts)
	}
	fs := &FontSet{entries: make([]fontEntry, 0, len(faces))}
	for i, face := range faces {
		if face.Index < 0 {
			return nil, fmt.Errorf("%w: face %d has negative index", ErrInvalidOptions, i)
		}
		if len(face.Data) == 0 {
			return nil, fmt.Errorf("%w: face %d has no data", ErrInvalidOptions, i)
		}
		if face.Slant != SlantNone && face.Slant != SlantItalic {
			return nil, fmt.Errorf("%w: face %d has unknown slant %d", ErrInvalidOptions, i, face.Slant)
		}
		weight := face.Weight
		if weight == 0 {
			weight = 400
		}
		if face.Family == "" {
			face.Family = "default"
		}

		data := face.Data
		loaders, err := ot.NewLoaders(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%w: face %d %q: %v", ErrInvalidOptions, i, face.Family, err)
		}
		if face.Index >= len(loaders) {
			return nil, fmt.Errorf("%w: face %d %q index %d out of range (%d faces)",
				ErrInvalidOptions, i, face.Family, face.Index, len(loaders))
		}
		parsed, err := font.NewFont(loaders[face.Index])
		if err != nil {
			return nil, fmt.Errorf("%w: face %d %q: %v", ErrInvalidOptions, i, face.Family, err)
		}
		fs.entries = append(fs.entries, fontEntry{
			ref:   &paint.Font{Family: face.Family, Data: data, Index: face.Index, Weight: weight},
			font:  parsed,
			index: weight<<8 | int(face.Slant),
		})
	}
	return fs, nil
}

// FontCount returns the number of faces in the set.
func (fs *FontSet) FontCount() int { return len(fs.entries) }

// order returns face indices ordered by preference for the requested family,
// weight and slant. Faces from other families remain available as fallbacks.
func (fs *FontSet) order(family string, weight int, slant Slant) []int {
	if weight == 0 {
		weight = 400
	}
	family = strings.TrimSpace(family)
	wantSlant := int(slant)

	type scored struct {
		idx   int
		fam   bool
		dist  int
		slant int
	}
	all := make([]scored, len(fs.entries))
	for i := range fs.entries {
		e := &fs.entries[i]
		fam := family != "" && strings.EqualFold(e.ref.Family, family)
		ew := e.index >> 8
		es := e.index & 0xff
		dist := ew - weight
		if dist < 0 {
			dist = -dist
		}
		slantPenalty := 0
		if es != wantSlant {
			slantPenalty = 1
		}
		all[i] = scored{idx: i, fam: fam, dist: dist, slant: slantPenalty}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.fam != b.fam {
			return a.fam
		}
		if a.slant != b.slant {
			return a.slant < b.slant
		}
		if a.dist != b.dist {
			return a.dist < b.dist
		}
		return a.idx < b.idx
	})
	out := make([]int, len(all))
	for i := range all {
		out[i] = all[i].idx
	}
	return out
}

// emulateBold reports whether a host should fake a bold face for a run.
//
// Font matching has already chosen the face carried by ref. When the requested
// weight is bold (>= 600) but the matched face is not (its actual weight is
// below 600), the core sets paint.GlyphRun.EmulateBold. It deliberately does
// not alter advances or synthesize outlines: whether to embolden, and how, is
// host policy.
func emulateBold(requested int, ref *paint.Font) bool {
	if requested < 600 {
		return false
	}
	actual := 400
	if ref != nil && ref.Weight > 0 {
		actual = ref.Weight
	}
	return actual < 600
}
