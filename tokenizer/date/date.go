package date

import (
	"iter"
	"time"
	"unsafe"

	"github.com/RogueTeam/textiplex/numeric"
	"github.com/RogueTeam/textiplex/tokenizer"
)

const DefaultDateLayout = time.DateOnly

func Tokenizer(in []byte) (seq iter.Seq[*tokenizer.Token]) {
	return func(yield func(*tokenizer.Token) bool) {
		if len(in) == 0 {
			return
		}
		v, err := time.Parse(DefaultDateLayout, unsafe.String(&in[0], len(in)))
		if err != nil {
			return
		}

		var tok = new(tokenizer.Token)
		tok.IsStem = true
		tok.Value = make([]byte, 8)
		numeric.PutSortableInteger(tok.Value, v.UnixNano())

		yield(tok)
	}
}
