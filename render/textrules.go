package render

// TextRules collects the document-wide policies that change line breaking or
// pagination. It is deliberately a plain struct of switches instead of a set of
// scattered fields: everything in here changes break or page results, so the
// layout cache hashes the whole value at once (see chapterBreakKey). A policy
// added here can therefore never be forgotten in a key.
//
// The zero value is the spec default: optional typographic policies are off.
// Nothing in this struct is correct by accident; hosts opt in.
type TextRules struct {
	// Autospacing inserts a small gap between an ideograph and a Latin letter
	// or digit, following CLREQ's recommendation. It is not a CSS initial
	// value, so it is off by default.
	Autospacing bool
	// PunctuationCompression trims half an em from the blank side of adjacent
	// fullwidth punctuation, following CSS Text L4 text-spacing-trim: normal.
	// It changes line breaking.
	PunctuationCompression bool
	// HangingPunctuation lets a closing punctuation mark that falls at the end
	// of a line hang into the right margin by up to half its advance, following
	// CSS Text L4 hanging-punctuation: allow-end. It changes line breaking.
	HangingPunctuation bool
	// Widows and Orphans are the minimum number of lines of a paragraph left
	// at the top and bottom of a page. Zero disables the protection, matching
	// CSS Fragmentation L3, which browsers largely do not implement either.
	Widows  int
	Orphans int
}
