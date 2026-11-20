//go:build js && wasm

package main

import (
	"bytes"
	"fmt"
	"strconv"
	"syscall/js"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/laic7092/layx/render/paint"
)

// canvasPainter implements paint.Painter on top of the Canvas 2D API.
//
// Text is drawn from the font's own outlines: layx gives us glyph IDs, so we
// load the contour segments through go-text and emit them as SVG path data
// into a Path2D. This keeps shaping and rasterization honest — the browser
// never re-shapes the string behind our back.
type canvasPainter struct {
	ctx  js.Value
	ctor js.Value // Path2D constructor

	face      *font.Face
	faceIndex int

	outlines map[uint32][]font.Segment
	images   map[paint.ImageID]js.Value

	buf      []byte
	lastFill string
}

func newCanvasPainter(ctx js.Value, face *font.Face, faceIndex int) *canvasPainter {
	return &canvasPainter{
		ctx:       ctx,
		ctor:      js.Global().Get("Path2D"),
		face:      face,
		faceIndex: faceIndex,
		outlines:  make(map[uint32][]font.Segment),
		images:    make(map[paint.ImageID]js.Value),
	}
}

func (p *canvasPainter) reset() {
	p.buf = p.buf[:0]
	p.lastFill = ""
}

func (p *canvasPainter) Save()    { p.ctx.Call("save") }
func (p *canvasPainter) Restore() { p.ctx.Call("restore") }

func (p *canvasPainter) ClipRect(r paint.Rect) {
	p.ctx.Call("beginPath")
	p.ctx.Call("rect", r.X, r.Y, r.Width, r.Height)
	p.ctx.Call("clip")
}

func (p *canvasPainter) Transform(m paint.Affine) {
	if m == paint.Identity() {
		return
	}
	p.ctx.Call("transform", m.A, m.B, m.C, m.D, m.E, m.F)
}

func (p *canvasPainter) FillRect(r paint.Rect, c paint.Color) {
	if c.Transparent() || r.Empty() {
		return
	}
	p.setFill(c)
	p.ctx.Call("fillRect", r.X, r.Y, r.Width, r.Height)
}

func (p *canvasPainter) StrokeRect(r paint.Rect, c paint.Color, width float32) {
	if c.Transparent() || r.Empty() || width <= 0 {
		return
	}
	p.ctx.Set("strokeStyle", cssColor(c))
	p.ctx.Set("lineWidth", width)
	p.ctx.Call("strokeRect", r.X, r.Y, r.Width, r.Height)
}

func (p *canvasPainter) FillRoundRect(r paint.Rect, radius float32, c paint.Color) {
	if c.Transparent() || r.Empty() {
		return
	}
	radius = minF32(radius, r.Width/2)
	radius = minF32(radius, r.Height/2)
	if radius <= 0 {
		p.FillRect(r, c)
		return
	}
	x, y, w, h := r.X, r.Y, r.Width, r.Height
	p.ctx.Call("beginPath")
	p.ctx.Call("moveTo", x+radius, y)
	p.ctx.Call("arcTo", x+w, y, x+w, y+h, radius)
	p.ctx.Call("arcTo", x+w, y+h, x, y+h, radius)
	p.ctx.Call("arcTo", x, y+h, x, y, radius)
	p.ctx.Call("arcTo", x, y, x+w, y, radius)
	p.ctx.Call("closePath")
	p.setFill(c)
	p.ctx.Call("fill")
}

func (p *canvasPainter) DrawImage(id paint.ImageID, dst paint.Rect, _ paint.ImageOptions) {
	img, ok := p.images[id]
	if !ok || img.IsUndefined() || dst.Empty() {
		return
	}
	p.ctx.Call("drawImage", img, dst.X, dst.Y, dst.Width, dst.Height)
}

func (p *canvasPainter) DrawGlyphs(run paint.GlyphRun) {
	if p.face == nil || run.Font == nil || len(run.Glyphs) == 0 || run.Color.Transparent() || run.Size <= 0 {
		return
	}
	upem := float32(p.face.Upem())
	if upem <= 0 {
		return
	}
	scale := run.Size / upem

	buf := p.buf[:0]
	for i := range run.Glyphs {
		g := &run.Glyphs[i]
		segs := p.outline(g.ID)
		if len(segs) == 0 {
			continue
		}
		ox := run.X + g.X
		oy := run.Y + g.Y
		for j := range segs {
			s := &segs[j]
			args := s.ArgsSlice()
			switch s.Op {
			case ot.SegmentOpMoveTo:
				buf = append(buf, 'M')
				buf = appendPoint(buf, ox+args[0].X*scale, oy-args[0].Y*scale)
			case ot.SegmentOpLineTo:
				buf = append(buf, 'L')
				buf = appendPoint(buf, ox+args[0].X*scale, oy-args[0].Y*scale)
			case ot.SegmentOpQuadTo:
				buf = append(buf, 'Q')
				buf = appendPoint(buf, ox+args[0].X*scale, oy-args[0].Y*scale)
				buf = append(buf, ' ')
				buf = appendPoint(buf, ox+args[1].X*scale, oy-args[1].Y*scale)
			case ot.SegmentOpCubeTo:
				buf = append(buf, 'C')
				buf = appendPoint(buf, ox+args[0].X*scale, oy-args[0].Y*scale)
				buf = append(buf, ' ')
				buf = appendPoint(buf, ox+args[1].X*scale, oy-args[1].Y*scale)
				buf = append(buf, ' ')
				buf = appendPoint(buf, ox+args[2].X*scale, oy-args[2].Y*scale)
			}
		}
		buf = append(buf, 'Z')
	}
	if len(buf) == 0 {
		return
	}
	p.buf = buf

	p.setFill(run.Color)
	p.ctx.Call("fill", p.ctor.New(string(buf)))
}

func (p *canvasPainter) outline(gid uint32) []font.Segment {
	if segs, ok := p.outlines[gid]; ok {
		return segs
	}
	out, _ := p.face.GlyphDataOutline(font.GID(gid))
	p.outlines[gid] = out.Segments
	return out.Segments
}

func (p *canvasPainter) setFill(c paint.Color) {
	s := cssColor(c)
	if s == p.lastFill {
		return
	}
	p.lastFill = s
	p.ctx.Set("fillStyle", s)
}

func appendPoint(buf []byte, x, y float32) []byte {
	buf = strconv.AppendFloat(buf, float64(x), 'f', 2, 32)
	buf = append(buf, ' ')
	buf = strconv.AppendFloat(buf, float64(y), 'f', 2, 32)
	return buf
}

func cssColor(c paint.Color) string {
	if c.A == 0xff {
		return "#" + hex2(c.R) + hex2(c.G) + hex2(c.B)
	}
	return fmt.Sprintf("rgba(%d,%d,%d,%.3f)", c.R, c.G, c.B, float32(c.A)/255)
}

func hex2(v uint8) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[v>>4], digits[v&0x0f]})
}

func minF32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

// newOutlineFace parses one face out of a font file. It is used both by the
// boot path (to pick a face) and by the painter (to draw glyphs).
func newOutlineFace(data []byte) ([]*font.Face, error) {
	return font.ParseTTC(bytes.NewReader(data))
}
