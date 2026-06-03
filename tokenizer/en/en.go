package en

import (
	"iter"
	"unicode"
	"unicode/utf8"

	"github.com/RogueTeam/textiplex/tokenizer"
)

// Tokenizer lowers tokens and strips a small set of inflectional
// suffixes. Deliberately light and lossy, tuned for recall. Use a Snowball
// port if you need linguistic accuracy.
func Tokenizer(in []byte) iter.Seq[*tokenizer.Token] {
	return tokenizer.TokenizeWithStemmer(in, Stemmer)
}

func TokenizerWithoutStopwords(in []byte) iter.Seq[*tokenizer.Token] {
	return tokenizer.FilterStopword(Stopwords, tokenizer.TokenizeWithStemmer(in, Stemmer))
}

func Stemmer(raw []byte) ([]byte, bool) {
	n := len(raw)
	keep := n
	switch {
	case n > 4 && tokenizer.HasSuffixFold(raw, "sses"):
		keep = n - 2 // sses -> ss
	case n > 4 && tokenizer.HasSuffixFold(raw, "ies"):
		keep = n - 2 // ies -> i
	case n > 4 && tokenizer.HasSuffixFold(raw, "ing") && HasVowel(raw[:n-3]):
		keep = Undouble(raw, n-3)
	case n > 3 && tokenizer.HasSuffixFold(raw, "ed") && HasVowel(raw[:n-2]):
		keep = Undouble(raw, n-2)
	case n > 4 && tokenizer.HasSuffixFold(raw, "ly"):
		keep = n - 2
	case n > 3 && tokenizer.HasSuffixFold(raw, "es"):
		keep = n - 2
	case n > 3 && tokenizer.HasSuffixFold(raw, "ss"):
		// trailing ss is real, do not strip
	case n > 2 && raw[n-1]|0x20 == 's':
		keep = n - 1
	}
	return tokenizer.FinishStemmer(raw, keep, Fold)
}

func Fold(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c < utf8.RuneSelf {
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			out = append(out, c)
			i++
			continue
		}
		r, size := utf8.DecodeRune(b[i:])
		out = utf8.AppendRune(out, unicode.ToLower(r))
		i += size
	}
	return out
}

// Undouble drops one of a trailing doubled consonant after a suffix strip,
// e.g. runn -> run. Still a prefix, so the result can stay borrowed.
func Undouble(raw []byte, keep int) int {
	if keep >= 2 {
		a, b := raw[keep-1]|0x20, raw[keep-2]|0x20
		if a == b && a != 'l' && a != 's' && a != 'z' {
			return keep - 1
		}
	}
	return keep
}

func HasVowel(b []byte) bool {
	for _, c := range b {
		switch c | 0x20 {
		case 'a', 'e', 'i', 'o', 'u', 'y':
			return true
		}
	}
	return false
}
