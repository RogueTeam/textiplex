package tokenizer_test

import (
	"testing"

	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/RogueTeam/textiplex/tokenizer/en"
	"github.com/stretchr/testify/assert"
)

func TestTokenPointerReused(t *testing.T) {
	type Test struct {
		name string
		in   string
	}

	tests := []Test{
		{name: "same pointer across the whole sequence", in: "the quick brown fox jumps"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var ptrs []*tokenizer.Token
			for tok := range en.EnglishTokenizer([]byte(tc.in)) {
				ptrs = append(ptrs, tok)
			}
			if !assertions.Greater(len(ptrs), 1, "need several tokens to prove reuse") {
				return
			}
			for i := 1; i < len(ptrs); i++ {
				assertions.Same(ptrs[0], ptrs[i], "Token pointer must be reused at index %d", i)
			}
		})
	}
}
