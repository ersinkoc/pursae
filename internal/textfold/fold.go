// Package textfold folds text for case-insensitive search that also works for Turkish and
// Azerbaijani, whose dotted and dotless i (i/İ, ı/I) the default Unicode case rules keep apart.
package textfold

import "strings"

// iFolder maps the letters the default lower-casing leaves apart from i: the dotless ı (U+0131),
// the dotted capital İ (U+0130) in case a lower-casing kept it, and the combining dot above
// (U+0307) that some lower-casings leave after the i of İ.
var iFolder = strings.NewReplacer("ı", "i", "İ", "i", "i̇", "i")

// Fold lower-cases s and folds every i-like letter to a plain i, so "IŞIK", "Işık" and "ışık" all
// give "işik". It is the same on every site language: a Turkish lower-casing would turn the English
// "INFO" into "ınfo", and would still miss text typed with the other spelling. It is for matching done
// in Go; text matched in SQL is folded there on both sides (store.FoldedTitleSQL, store.FoldedArgSQL,
// migration 0105), so the term and the text share the database collation's lower-casing.
func Fold(s string) string {
	return iFolder.Replace(strings.ToLower(s))
}

// Contains reports whether term occurs in s, ignoring case and the dotted/dotless i.
func Contains(s, term string) bool {
	return strings.Contains(Fold(s), Fold(term))
}
