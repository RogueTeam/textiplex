package es

import (
	"iter"
	"unicode"
	"unicode/utf8"

	"github.com/RogueTeam/textiplex/tokenizer"
)

// SpanishTokenizer lowers tokens, folds accents (keeping ñ) and strips common
// plural and adverb suffixes. Light and lossy, not Snowball.
func SpanishTokenizer(in []byte) iter.Seq[*tokenizer.Token] {
	return tokenizer.TokenizeWithStemmer(in, SpanishStemmer)
}

func SpanishStemmer(raw []byte) ([]byte, bool) {
	// Suffixes we strip are ASCII, so matching on raw is safe even when earlier
	// bytes are multibyte: accent folding only rewrites those earlier bytes.
	n := len(raw)
	keep := n
	switch {
	case n > 6 && tokenizer.HasSuffixFold(raw, "mente"):
		keep = n - 5
	case n > 4 && tokenizer.HasSuffixFold(raw, "es"):
		keep = n - 2
	case n > 3 && raw[n-1]|0x20 == 's':
		keep = n - 1
	}
	return tokenizer.FinishStemmer(raw, keep, foldSpanish)
}

func foldSpanish(b []byte) []byte {
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
		r = unicode.ToLower(r)
		switch r {
		case 'á':
			r = 'a'
		case 'é':
			r = 'e'
		case 'í':
			r = 'i'
		case 'ó':
			r = 'o'
		case 'ú', 'ü':
			r = 'u'
		} // ñ left untouched, it is a distinct letter
		out = utf8.AppendRune(out, r)
		i += size
	}
	return out
}
