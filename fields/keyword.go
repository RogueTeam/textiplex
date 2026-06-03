package fields

import (
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/zeebo/xxh3"
)

func KeywordField(dst *storage.FieldDefinition, tokPool *pool.Pool[storage.TokenDefinition], name string, value []byte) {
	dst.Hash = xxh3.HashString(name)
	dst.Length = 1

	token := tokPool.Get()
	token.Frequency = 1
	token.Value = value
	dst.Tokens = []*storage.TokenDefinition{token}
}
