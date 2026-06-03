package tokenizer

import "iter"

type Token struct {
	Value  []byte
	IsStem bool
}

type Tokenizer func(in []byte) (seq iter.Seq[*Token])
