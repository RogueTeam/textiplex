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
	// Tokens whose hash is already taken by a different value. xxh3 is not
	// collision resistant, so whoever controls the indexed text could
	// otherwise make one token silently count as another.
	var collided map[string]*storage.TokenDefinition
	var tokensSize uint64
	for rawToken := range tokenizer(text) {
		if rawToken == nil || len(rawToken.Value) == 0 {
			continue
		}

		dst.Length++

		tokenHash := xxh3.Hash(rawToken.Value)

		token, found := tokensMap[tokenHash]
		if found && !bytes.Equal(token.Value, rawToken.Value) {
			if collided == nil {
				collided = make(map[string]*storage.TokenDefinition)
			}
			token, found = collided[string(rawToken.Value)]
			if !found {
				token = tokPool.Get()
				wideTokens = append(wideTokens, token)
				*token = storage.TokenDefinition{Value: rawToken.Value}
				collided[string(rawToken.Value)] = token
				tokensSize += TokenSize(token)
			}
		} else if !found {
			token = tokPool.Get()
			wideTokens = append(wideTokens, token)

			*token = storage.TokenDefinition{
				Value:     rawToken.Value,
				Frequency: 0,
			}
			tokensMap[tokenHash] = token
			tokensSize += TokenSize(token)
		}

		token.Frequency++
	}

	dst.Tokens = make([]*storage.TokenDefinition, 0, len(wideTokens))
	dst.Tokens = append(dst.Tokens, wideTokens...)
	slices.SortFunc(dst.Tokens, func(a, b *storage.TokenDefinition) int { return bytes.Compare(a.Value, b.Value) })

	return BaseFieldDefinitionSize(dst) + tokensSize
}
