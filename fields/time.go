package fields

import (
	"time"

	"github.com/RogueTeam/textiplex/numeric"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/zeebo/xxh3"
)

func TimeField(dst *storage.FieldDefinition, tokPool *pool.Pool[storage.TokenDefinition], name string, t time.Time) (size uint64) {
	dst.Hash = xxh3.HashString(name)
	dst.Length = 1

	token := tokPool.Get()
	token.Frequency = 1
	token.Value = make([]byte, 8)
	dst.Tokens = []*storage.TokenDefinition{token}

	numeric.PutSortableInteger(token.Value, t.UnixNano())

	return BaseFieldDefinitionSize(dst) + TokenSize(token)
}
