package floating

import (
	"iter"
	"strconv"
	"unsafe"

	"github.com/RogueTeam/textiplex/numeric"
	"github.com/RogueTeam/textiplex/tokenizer"
)

func Tokenizer(in []byte) (seq iter.Seq[*tokenizer.Token]) {
	return func(yield func(*tokenizer.Token) bool) {
		if len(in) == 0 {
			return
		}
		v, err := strconv.ParseFloat(unsafe.String(&in[0], len(in)), 64)
		if err != nil {
			return
		}

		var tok = new(tokenizer.Token)
		tok.IsStem = true
		tok.Value = make([]byte, 8)
		numeric.PutSortableFloat(tok.Value, v)

		yield(tok)
	}
}
