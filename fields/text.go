package fields

import (
	"bytes"
	"slices"

	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/zeebo/xxh3"
)

func TextField(dst *storage.FieldDefinition, tokPool *pool.Pool[storage.TokenDefinition], name string, text []byte, tokenizer tokenizer.Tokenizer) (size uint64) {
	dst.Hash = xxh3.HashString(name)
	dst.Length = 0

	var wideTokens []*storage.TokenDefinition
	tokensMap := make(map[uint64]*storage.TokenDefinition)
	var tokensSize uint64
	for rawToken := range tokenizer(text) {
		if !rawToken.IsStem {
			dst.Length++
		}

		tokenHash := xxh3.Hash(rawToken.Value)

		token, found := tokensMap[tokenHash]
		if !found {
			token = tokPool.Get()
			*token = storage.TokenDefinition{
				Value:     rawToken.Value,
				Frequency: 0,
			}
			tokensMap[tokenHash] = token
			tokensSize += TokenSize(token)
			wideTokens = append(wideTokens, token)
		}

		token.Frequency++
	}

	dst.Tokens = make([]*storage.TokenDefinition, len(wideTokens))
	copy(dst.Tokens, wideTokens)
	slices.SortFunc(dst.Tokens, func(a, b *storage.TokenDefinition) int { return bytes.Compare(a.Value, b.Value) })

	return BaseFieldDefinitionSize(dst) + tokensSize
}
