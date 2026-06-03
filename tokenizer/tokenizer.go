package tokenizer

import (
	"iter"
	"unicode"
	"unicode/utf8"
)

type Token struct {
	Value  []byte
	IsStem bool
}

type Tokenizer func(in []byte) (seq iter.Seq[*Token])

// Stemmer normalizes one raw token. It returns the term and whether the term is
// an owned allocation (true) or a sub slice of raw (false).
type Stemmer func(raw []byte) (term []byte, owned bool)

func TokenizeWithStemmer(in []byte, stem Stemmer) iter.Seq[*Token] {
	return func(yield func(*Token) bool) {
		// One Token reused for the whole sequence. The pointer and its Value are
		// valid only until the next iteration. Copy if you need to retain them.
		tok := new(Token)
		n := len(in)
		for i := 0; i < n; {
			r, size := utf8.DecodeRune(in[i:])
			if !isWord(r) {
				i += size
				continue
			}
			end := wordEnd(in, i)
			tok.Value, tok.IsStem = stem(in[i:end])
			if !yield(tok) {
				return
			}
			i = end
		}
	}
}

// wordEnd returns the index past the token that starts at i. A token runs over
// letters and digits. A separator (. , : - /) is kept only when it sits between
// two digits, so decimals, dates, times and thousands stay whole.
func wordEnd(in []byte, i int) int {
	n := len(in)
	r, size := utf8.DecodeRune(in[i:])
	prevDigit := unicode.IsDigit(r)
	i += size
	for i < n {
		r, size = utf8.DecodeRune(in[i:])
		switch {
		case isWord(r):
			prevDigit = unicode.IsDigit(r)
			i += size
		case prevDigit && isNumSep(r) && nextIsDigit(in[i+size:]):
			prevDigit = false
			i += size
		default:
			return i
		}
	}
	return i
}

func HasSuffixFold(b []byte, suf string) bool {
	if len(b) < len(suf) {
		return false
	}
	b = b[len(b)-len(suf):]
	for i := 0; i < len(suf); i++ {
		c := b[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != suf[i] {
			return false
		}
	}
	return true
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func isNumSep(r rune) bool {
	switch r {
	case '.', ',', ':', '-', '/':
		return true
	}
	return false
}

func nextIsDigit(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	r, _ := utf8.DecodeRune(b)
	return unicode.IsDigit(r)
}

// FinishStemmer returns a borrowed sub slice when the kept prefix needs no rewriting,
// otherwise the folded allocation. fold lowers and, for Spanish, strips accents.
func FinishStemmer(raw []byte, keep int, fold func([]byte) []byte) ([]byte, bool) {
	stem := raw[:keep]
	if !NeedsFold(stem) {
		return stem, false
	}
	return fold(stem), true
}

// NeedsFold reports whether b contains an ASCII uppercase letter or any
// multibyte rune, the only cases that force an allocation.
func NeedsFold(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf || ('A' <= c && c <= 'Z') {
			return true
		}
	}
	return false
}
