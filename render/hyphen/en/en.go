package en

import "strings"

// Hyphenator implements Liang's pattern-matching algorithm for American
// English. It is safe for concurrent use.
type Hyphenator struct{}

// New returns the built-in English (en-us) hyphenator.
func New() Hyphenator { return Hyphenator{} }

// Name identifies the pattern set in the layout cache key.
func (Hyphenator) Name() string { return "en-us" }

// Breaks returns the rune offsets inside word after which a hyphen may be
// inserted. Offsets are zero-based and ascending, and both the left and right
// fragments respect the TeX hyphenmins (2 and 3) for English.
func (Hyphenator) Breaks(word []rune) []int {
	if len(word) < 5 {
		return nil
	}
	letters := make([]byte, len(word))
	for i, r := range word {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r < 'a' || r > 'z' {
			return nil
		}
		letters[i] = byte(r)
	}

	// Liang's algorithm works on the word surrounded by dots, so patterns can
	// anchor at the beginning or end.
	dotted := make([]byte, 0, len(letters)+2)
	dotted = append(dotted, '.')
	dotted = append(dotted, letters...)
	dotted = append(dotted, '.')

	// weights[i] is the odd/even score at the position before dotted[i].
	weights := make([]uint8, len(dotted)+1)
	for start := 0; start < len(dotted); start++ {
		n := &patternRoot
		for j := start; j < len(dotted); j++ {
			n = n.children[dotted[j]]
			if n == nil {
				break
			}
			if len(n.weights) == 0 {
				continue
			}
			for k, w := range n.weights {
				idx := start + k
				if idx < len(weights) && w > weights[idx] {
					weights[idx] = w
				}
			}
		}
	}

	var out []int
	// A break after i letters sits at dotted index i+1. English hyphenmins:
	// at least two letters before and three after the hyphen.
	for i := 2; i <= len(letters)-3; i++ {
		if weights[i+1]%2 == 1 {
			out = append(out, i)
		}
	}
	return out
}

type patternNode struct {
	children map[byte]*patternNode
	// weights has len(letters)+1 entries; weights[k] is the score before the
	// k-th letter of the matched pattern.
	weights []uint8
}

func newPatternNode() *patternNode {
	return &patternNode{children: make(map[byte]*patternNode, 4)}
}

// patternRoot is built once during package initialization and is immutable
// afterwards. It is the only package-level state.
var patternRoot = buildPatterns(strings.Split(patternData, "\n"))

func buildPatterns(lines []string) patternNode {
	root := newPatternNode()
	for _, line := range lines {
		if line == "" {
			continue
		}
		letters := make([]byte, 0, len(line))
		weights := []uint8{0}
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch {
			case c >= '0' && c <= '9':
				weights[len(weights)-1] = c - '0'
			case c == '.' || (c >= 'a' && c <= 'z'):
				letters = append(letters, c)
				weights = append(weights, 0)
			}
		}
		if len(letters) == 0 {
			continue
		}
		n := root
		for _, c := range letters {
			child := n.children[c]
			if child == nil {
				child = newPatternNode()
				n.children[c] = child
			}
			n = child
		}
		if len(n.weights) == 0 {
			n.weights = weights
			continue
		}
		for i, w := range weights {
			if w > n.weights[i] {
				n.weights[i] = w
			}
		}
	}
	return *root
}
