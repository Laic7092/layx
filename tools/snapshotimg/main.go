// Command snapshotimg composes and compares rendering snapshots.
//
// It is a development tool for scripts/snapshot.sh, not part of the library.
//
//	snapshotimg side -gap 24 -out compare.png a.png b.png [c.png ...]
//	snapshotimg diff -out diff.png a.png b.png
//	snapshotimg stats a.png b.png
//
// Inputs must share the same dimensions. Everything is stdlib-only so the tool
// adds no dependencies to the module.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "side":
		cmdSide(os.Args[2:])
	case "diff":
		cmdDiff(os.Args[2:])
	case "stats":
		cmdStats(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: snapshotimg side|diff|stats [flags] <input.png>...")
	os.Exit(2)
}

func load(path string) image.Image {
	f, err := os.Open(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		fatal(fmt.Errorf("%s: %w", path, err))
	}
	return img
}

func save(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "snapshotimg:", err)
	os.Exit(1)
}

func cmdSide(args []string) {
	fs := flag.NewFlagSet("side", flag.ExitOnError)
	gap := fs.Int("gap", 24, "gap between images in pixels")
	bg := fs.String("bg", "#ffffff", "gap colour")
	out := fs.String("out", "", "output PNG (required)")
	fs.Parse(args)
	if *out == "" || fs.NArg() < 2 {
		usage()
	}

	imgs := make([]image.Image, 0, fs.NArg())
	for _, p := range fs.Args() {
		imgs = append(imgs, load(p))
	}
	w, h := 0, 0
	for _, im := range imgs {
		b := im.Bounds()
		w += b.Dx()
		h = max(h, b.Dy())
	}
	w += *gap * (len(imgs) - 1)

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(parseColor(*bg)), image.Point{}, draw.Src)
	x := 0
	for _, im := range imgs {
		b := im.Bounds()
		r := image.Rect(x, 0, x+b.Dx(), b.Dy())
		draw.Draw(dst, r, im, b.Min, draw.Src)
		x += b.Dx() + *gap
	}
	save(*out, dst)
}

func cmdDiff(args []string) {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	out := fs.String("out", "", "output PNG (required)")
	fs.Parse(args)
	if *out == "" || fs.NArg() != 2 {
		usage()
	}
	a, b := load(fs.Arg(0)), load(fs.Arg(1))
	if a.Bounds().Dx() != b.Bounds().Dx() || a.Bounds().Dy() != b.Bounds().Dy() {
		fatal(fmt.Errorf("size mismatch: %v vs %v", a.Bounds(), b.Bounds()))
	}
	w, h := a.Bounds().Dx(), a.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := diffPixel(a.At(x, y), b.At(x, y))
			// Amplify small differences so they are visible in the diff image.
			v := uint8(min(255, d*3))
			dst.SetRGBA(x, y, color.RGBA{R: v, G: v / 3, B: v / 3, A: 0xff})
		}
	}
	save(*out, dst)
}

func cmdStats(args []string) {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 2 {
		usage()
	}
	a, b := load(fs.Arg(0)), load(fs.Arg(1))
	if a.Bounds().Dx() != b.Bounds().Dx() || a.Bounds().Dy() != b.Bounds().Dy() {
		fatal(fmt.Errorf("size mismatch: %v vs %v", a.Bounds(), b.Bounds()))
	}
	w, h := a.Bounds().Dx(), a.Bounds().Dy()
	changed, total, sum := 0, w*h, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := diffPixel(a.At(x, y), b.At(x, y))
			sum += d
			if d > 8 {
				changed++
			}
		}
	}
	fmt.Printf("pixels=%d changed=%d changed_pct=%.2f mean_abs_diff=%.3f\n",
		total, changed, float64(changed)*100/float64(total), float64(sum)/float64(total))
}

func diffPixel(a, b color.Color) int {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	d := absDiff(ar, br) + absDiff(ag, bg) + absDiff(ab, bb) + absDiff(aa, ba)
	return int(d / 4 / 257)
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

func parseColor(s string) color.Color {
	var r, g, b uint8
	if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return color.White
	}
	return color.RGBA{R: r, G: g, B: b, A: 0xff}
}
