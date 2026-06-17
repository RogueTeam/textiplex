package textiplex_test

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/RogueTeam/textiplex"
	"github.com/RogueTeam/textiplex/fields"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

const WriterDocumentCount int64 = 1_000_000

func BenchmarkWriter(b *testing.B) {
	assertions := assert.New(b)

	b.StopTimer()

	tokPool := pool.New[storage.TokenDefinition](20)
	fieldsPool := pool.New[storage.FieldDefinition](30)

	batch := fields.NewBatch(50)
	for index := range WriterDocumentCount {
		var totalFieldSize uint64

		fieldDef1 := fieldsPool.Get()
		fieldDef2 := fieldsPool.Get()
		fieldDef3 := fieldsPool.Get()

		idxS := strconv.FormatInt(index, 10)
		name := []byte("hello-" + idxS)

		totalFieldSize += fields.KeywordField(fieldDef1, tokPool, "name", name)
		totalFieldSize += fields.KeywordField(fieldDef2, tokPool, "index", name[len("hello-"):])
		totalFieldSize += fields.KeywordField(fieldDef3, tokPool, "reversed-name", []byte("olleh-"+idxS))
		batch.Insert(storage.DocumentId{Value: storage.RawValueFrom(fmt.Appendf(nil, "%d", index))}, totalFieldSize, fieldDef1, fieldDef2, fieldDef3)
	}
	b.ResetTimer()
	b.ReportAllocs()
	b.StartTimer()

	for b.Loop() {
		b.StopTimer()

		var writer = textiplex.Writer{
			TemporaryDirectory: testsuite.TempDirectory(b, "tmp-*"),
			Directory:          testsuite.TempDirectory(b, "data-*"),
		}

		b.StartTimer()
		err := writer.Batch(batch)
		if !assertions.NoError(err, "batch should succeed") {
			return
		}

		err = writer.Merge()
		if !assertions.NoError(err, "merge should succeed") {
			return
		}
		b.StopTimer()

		os.RemoveAll(writer.TemporaryDirectory)
		os.RemoveAll(writer.Directory)
		b.StartTimer()
	}
}
