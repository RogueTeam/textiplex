package fields

import (
	"github.com/RogueTeam/textiplex/numeric"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/zeebo/xxh3"
	"golang.org/x/exp/constraints"
)

func IntegerField[T constraints.Signed](dst *storage.FieldDefinition, tokPool *pool.Pool[storage.TokenDefinition], name string, v T) {
	dst.Hash = xxh3.HashString(name)
	dst.Length = 1

	token := tokPool.Get()
	token.Frequency = 1
	token.Value = make([]byte, 8)
	dst.Tokens = []*storage.TokenDefinition{token}

	numeric.PutSortableInteger(token.Value, v)
}

func FloatField[T constraints.Float](dst *storage.FieldDefinition, tokPool *pool.Pool[storage.TokenDefinition], name string, v T) {
	dst.Hash = xxh3.HashString(name)
	dst.Length = 1

	token := tokPool.Get()
	token.Frequency = 1
	token.Value = make([]byte, 8)
	dst.Tokens = []*storage.TokenDefinition{token}

	numeric.PutSortableFloat(token.Value, v)
}
