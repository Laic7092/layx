//go:build ignore

// compare reads the PGM dumps ftdump and the fidelity test write and prints, per
// glyph and on average, how far each rasterizer is from a 16x16 supersampled
// ground truth. Build-ignored: it is a yardstick, not a tool the module ships.
//
//	go run compare.go ours-*.pgm ...   (see README.md for the whole run)
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func readPGM(p string) ([]uint8, int) {
	f, err := os.Open(p)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var magic string
	var w, h, max int
	fmt.Fscan(r, &magic, &w, &h, &max)
	r.ReadByte()
	pix := make([]uint8, w*h)
	n := 0
	for n < len(pix) {
		m, err := r.Read(pix[n:])
		if err != nil {
			break
		}
		n += m
	}
	return pix, w
}

type gid int

func dump(dir, prefix string, g gid) []uint8 {
	pix, _ := readPGM(filepath.Join(dir, prefix+"-"+strconv.Itoa(int(g))+".pgm"))
	return pix
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run compare.go <dir with ours-/ft-/ref-*.pgm>")
		os.Exit(2)
	}
	dir := os.Args[1]
	entries, err := filepath.Glob(filepath.Join(dir, "ref-*.pgm"))
	if err != nil || len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "no ref-*.pgm in %s\n", dir)
		os.Exit(1)
	}
	var gids []gid
	for _, e := range entries {
		base := strings.TrimSuffix(filepath.Base(e), ".pgm")
		n, err := strconv.Atoi(strings.TrimPrefix(base, "ref-"))
		if err != nil {
			continue
		}
		gids = append(gids, gid(n))
	}
	sort.Slice(gids, func(a, b int) bool { return gids[a] < gids[b] })

	type res struct{ mean, ink float64 }
	sum := map[string]res{}
	worst := map[string]res{}
	var n int
	for _, g := range gids {
		ref := dump(dir, "ref", g)
		if ref == nil {
			continue
		}
		var truth float64
		for _, v := range ref {
			truth += float64(v)
		}
		n++
		for _, name := range []string{"ours", "ft"} {
			got := dump(dir, name, g)
			if got == nil {
				continue
			}
			var diff, ink float64
			for i := range ref {
				d := float64(got[i]) - float64(ref[i])
				if d < 0 {
					d = -d
				}
				diff += d
				ink += float64(got[i])
			}
			r := res{mean: diff / float64(len(ref)), ink: ink / truth}
			s := sum[name]
			sum[name] = res{mean: s.mean + r.mean, ink: s.ink + r.ink}
			if w, ok := worst[name]; !ok || r.mean > w.mean {
				worst[name] = r
			}
		}
	}
	if n == 0 {
		fmt.Fprintln(os.Stderr, "no glyph triples found")
		os.Exit(1)
	}
	fmt.Printf("%d glyphs against 16x16 supersampled truth\n", n)
	fmt.Printf("%-14s %10s %10s\n", "", "mean|err|", "ink/truth")
	for _, name := range []string{"ours", "ft"} {
		s, ok := sum[name]
		if !ok {
			continue
		}
		label := "layx-raster"
		if name == "ft" {
			label = "freetype"
		}
		fmt.Printf("%-14s %10.3f %10.4f\n", label, s.mean/float64(n), s.ink/float64(n))
	}
}
