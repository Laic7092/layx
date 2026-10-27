package raster

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
)

// TestDumpReferencePGM writes the PGM pairs reference/freetype/ compares: for
// every gid in $LAYX_GIDS, this package's coverage and a 16x16 supersampled
// ground truth of the same outline, on an 80x80 canvas with the pen at (20,20).
// ftdump writes the third of the triple on the same canvas.
//
// It is skipped unless asked for, because it is a yardstick rather than a test:
//
//	LAYX_GIDS=gids.txt LAYX_FONT=font.ttc LAYX_DUMP=/tmp/ref \
//	    LAYX_PX=34 go test -run TestDumpReferencePGM .
func TestDumpReferencePGM(t *testing.T) {
	list := os.Getenv("LAYX_GIDS")
	if list == "" {
		t.Skip("set LAYX_GIDS to a file of gids to dump")
	}
	fontPath := os.Getenv("LAYX_FONT")
	out := os.Getenv("LAYX_DUMP")
	if out == "" {
		out = os.TempDir()
	}
	px := 34.0
	if v, err := strconv.ParseFloat(os.Getenv("LAYX_PX"), 64); err == nil && v > 0 {
		px = v
	}

	data, err := os.ReadFile(fontPath)
	if err != nil {
		t.Fatalf("read font: %v", err)
	}
	faces, err := font.ParseTTC(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse font: %v", err)
	}
	face := faces[0]
	scale := float32(px) / float32(face.Upem())

	f, err := os.Open(list)
	if err != nil {
		t.Fatalf("read gids: %v", err)
	}
	defer f.Close()
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	const (
		w, h = 80, 80
		pen  = 20
		ss   = 16
	)
	var cov Coverage
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		gid64, err := strconv.ParseUint(sc.Text(), 10, 32)
		if err != nil {
			continue
		}
		gid := uint32(gid64)
		glyph, ok := face.GlyphDataOutline(font.GID(gid))
		if !ok {
			continue
		}
		cov.Reset(w, h)
		pt := func(p ot.SegmentPoint) (float32, float32) {
			return float32(pen) + float32(p.X)*scale, float32(pen) - float32(p.Y)*scale
		}
		for _, seg := range glyph.Segments {
			args := seg.ArgsSlice()
			switch seg.Op {
			case ot.SegmentOpMoveTo:
				cov.MoveTo(pt(args[0]))
			case ot.SegmentOpLineTo:
				cov.LineTo(pt(args[0]))
			case ot.SegmentOpQuadTo:
				bx, by := pt(args[0])
				cx, cy := pt(args[1])
				cov.QuadTo(bx, by, cx, cy)
			case ot.SegmentOpCubeTo:
				bx, by := pt(args[0])
				cx, cy := pt(args[1])
				dx, dy := pt(args[2])
				cov.CubeTo(bx, by, cx, cy, dx, dy)
			}
		}
		writePGM(filepath.Join(out, fmt.Sprintf("ours-%d.pgm", gid)), w, h, cov.Alpha())
		writePGM(filepath.Join(out, fmt.Sprintf("ref-%d.pgm", gid)), w, h, supersample(&cov.shape, w, h, ss))
		n++
	}
	t.Logf("wrote %d glyph pairs to %s", n, out)
}

func writePGM(path string, w, h int, pix []uint8) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "P5\n%d %d\n255\n", w, h)
	f.Write(pix)
}
