package fields_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/RogueTeam/textiplex/fields"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
)

func TestBatchConstruction(t *testing.T) {

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	const docCount = 1_000_000
	batch := fields.NewBatch(docCount)

	// Pools are useful to allocate more easily the amount of fields the document needs in a single memory batch
	const fieldCount = 5
	var fieldsPool = pool.New[storage.FieldDefinition](fieldCount)
	var tokenPool = pool.New[storage.TokenDefinition](5)
	for i := range docCount {
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

		batch.Insert(fmt.Appendf(nil, "id-%d", i), totalFieldsSize, fieldsPtrs...)
	}

	runtime.ReadMemStats(&after)

	t.Logf("Memory usage before batch: %d", before.Alloc)
	t.Logf("Batch size: %d", batch.Size)
	t.Logf("Memory usage after batch: %d", after.Alloc)
	t.Logf("Memory diff between before and after batch: %d", after.Alloc-before.Alloc)
}
