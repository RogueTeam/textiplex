package fields_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/RogueTeam/textiplex/fields"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/dustin/go-humanize"
	"github.com/stretchr/testify/assert"
)

func TestBatchConstruction(t *testing.T) {
	assertions := assert.New(t)

	var before, after, final runtime.MemStats
	runtime.ReadMemStats(&before)

	batch := fields.NewBatch(20)

	// Pools are useful to allocate more easily the amount of fields the document needs in a single memory batch
	const fieldCount = 5
	var fieldsPool = pool.New[storage.FieldDefinition](fieldCount)
	var tokenPool = pool.New[storage.TokenDefinition](5)

	var i int
	for batch.Size < 1024*1024*1024 {
		var fieldsPtrs = make([]*storage.FieldDefinition, 0, fieldCount)

		// Personal id
		pidField := fieldsPool.Get()
		fieldsPtrs = append(fieldsPtrs, pidField)
		totalFieldsSize := fields.IntegerField(pidField, tokenPool, "age", 127997298+i)
		// Age
		ageField := fieldsPool.Get()
		fieldsPtrs = append(fieldsPtrs, ageField)
		totalFieldsSize += fields.IntegerField(ageField, tokenPool, "age", 30-i)
		// Country
		countryField := fieldsPool.Get()
		fieldsPtrs = append(fieldsPtrs, countryField)
		totalFieldsSize += fields.KeywordField(countryField, tokenPool, "country", []byte("Colombia"))
		// City
		cityField := fieldsPool.Get()
		fieldsPtrs = append(fieldsPtrs, cityField)
		totalFieldsSize += fields.KeywordField(cityField, tokenPool, "city", []byte("Bogota"))
		// Gender
		genderField := fieldsPool.Get()
		fieldsPtrs = append(fieldsPtrs, genderField)
		if i%2 == 0 {
			totalFieldsSize += fields.KeywordField(genderField, tokenPool, "gender", []byte("Male"))
		} else {
			totalFieldsSize += fields.KeywordField(genderField, tokenPool, "gender", []byte("Male"))
		}

		batch.Insert(storage.DocumentId{Value: storage.RawValueFrom(fmt.Appendf(nil, "id-%d", i))}, totalFieldsSize, fieldsPtrs...)

		i++
	}

	runtime.ReadMemStats(&after)

	t.Logf("==================== MEMORY USAGE ====================")
	t.Logf("Memory usage before batch: %s", humanize.Bytes(before.Alloc))
	t.Logf("Final batch size: %s", humanize.Bytes(batch.Size))
	t.Logf("Memory usage after batch: %s", humanize.Bytes(after.Alloc))
	t.Logf("Memory diff between before and after batch: %s", humanize.Bytes(after.Alloc-before.Alloc))

	var s storage.Storage
	s.BuildFrom(batch.Documents...)
	err := s.SaveTo(testsuite.TempFilename(t, "storage-%.seg"))
	if !assertions.NoError(err, "failed to save batch into storage") {
		return
	}

	runtime.ReadMemStats(&final)

	t.Logf("==================== MEMORY USAGE AFTER STORAGE CONSTRUCTION ====================")
	t.Logf("Memory usage after batch: %s", humanize.Bytes(after.Alloc))
	t.Logf("Memory usage after storage creation: %s", humanize.Bytes(final.Alloc))
	t.Logf("Memory diff between after and final: %s", humanize.Bytes(final.Alloc-after.Alloc))

}
