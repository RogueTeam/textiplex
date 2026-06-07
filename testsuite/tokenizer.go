package testsuite

import (
	"testing"
	"time"
	"unsafe"

	"github.com/RogueTeam/textiplex/numeric"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/stretchr/testify/assert"
)

type Term struct {
	Value string
	Owned bool
}

// AliasesFor reports whether v points inside the backing array of in. Borrowed
// tokens must alias the input, owned tokens must not.
func AliasesFor(in, v []byte) bool {
	if len(in) == 0 || len(v) == 0 {
		return false
	}
	base := uintptr(unsafe.Pointer(&in[0]))
	p := uintptr(unsafe.Pointer(&v[0]))
	return p >= base && p < base+uintptr(len(in))
}

// CollectTerms drains the sequence and asserts the ownership invariant on every
// token: IsStem is true exactly when Value is an owned allocation that does not
// alias the input. The Token pointer is reused, so values are copied out here.
func CollectTerms(t *testing.T, fn tokenizer.Tokenizer, in []byte) []Term {
	t.Helper()
	assertions := assert.New(t)
	out := make([]Term, 0)
	for tok := range fn(in) {
		alias := AliasesFor(in, tok.Value)
		if tok.IsStem {
			assertions.False(alias, "token %q has IsStem=true but aliases input", tok.Value)
		} else {
			assertions.True(alias, "token %q has IsStem=false but does not alias input", tok.Value)
		}
		out = append(out, Term{Value: string(tok.Value), Owned: tok.IsStem})
	}
	return out
}

func AssertTerms(t *testing.T, fn tokenizer.Tokenizer, in string, want []Term) {
	t.Helper()
	assertions := assert.New(t)

	got := CollectTerms(t, fn, []byte(in))
	if !assertions.Len(got, len(want), "token count for %q", in) {
		return
	}

	for i := range want {
		i := i
		t.Run(want[i].Value, func(t *testing.T) {
			assertions := assert.New(t)
			assertions.Equal(want[i].Value, got[i].Value, "value at index %d", i)
			assertions.Equal(want[i].Owned, got[i].Owned, "IsStem at index %d (%q)", i, got[i].Value)
		})
	}
}

// SortableInt64 encodes v with the same sortable byte layout production uses for
// integer fields, so token byte order matches numeric order.
func SortableInt64(v int64) []byte {
	buf := make([]byte, 8)
	numeric.PutSortableInteger(buf, v)
	return buf
}

// SortableFloat64 is the float counterpart of sortableInt.
func SortableFloat64(v float64) []byte {
	buf := make([]byte, 8)
	numeric.PutSortableFloat(buf, v)
	return buf
}

func SortableDate(t *testing.T, s string) []byte {
	t.Helper()
	assertions := assert.New(t)
	tm, err := time.Parse(time.DateOnly, s)
	assertions.Nil(err, "parse date %q", s)
	return SortableInt64(tm.UnixNano())
}
