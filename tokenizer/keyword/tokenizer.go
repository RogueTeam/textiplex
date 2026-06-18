package keyword

import (
	"iter"

	"github.com/RogueTeam/textiplex/tokenizer"
)

func Tokenizer(in []byte) (seq iter.Seq[*tokenizer.Token]) {
	return func(yield func(*tokenizer.Token) bool) {
		yield(&tokenizer.Token{Value: in})
	}
}

var _ tokenizer.Tokenizer = Tokenizer
