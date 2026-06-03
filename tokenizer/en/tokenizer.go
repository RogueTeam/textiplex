package en

import (
	"iter"

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
