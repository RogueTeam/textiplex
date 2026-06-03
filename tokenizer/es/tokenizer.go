package es

import (
	"iter"

	"github.com/RogueTeam/textiplex/tokenizer"
)

// Tokenizer lowers tokens, folds accents (keeping ñ) and strips common
// plural and adverb suffixes. Light and lossy, not Snowball.
func Tokenizer(in []byte) iter.Seq[*tokenizer.Token] {
	return tokenizer.TokenizeWithStemmer(in, Stemmer)
}

func TokenizerWithoutStopwords(in []byte) iter.Seq[*tokenizer.Token] {
	return tokenizer.FilterStopword(Stopwords, tokenizer.TokenizeWithStemmer(in, Stemmer))
}
