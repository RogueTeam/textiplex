package writer_test

import (
	"fmt"
	"testing"

	"github.com/pluto-org-co/bluge"
	"github.com/pluto-org-co/bluge/documents"
	"github.com/pluto-org-co/bluge/index"
	"github.com/pluto-org-co/bluge/testsuite"
	"github.com/stretchr/testify/assert"
)

const WriterDocumentCount = 1_000_000

func BenchmarkOfflineWriter(b *testing.B) {
	assertions := assert.New(b)

	b.StopTimer()
	batch := index.NewBatch()
	for index := range WriterDocumentCount {
		doc := documents.NewDocument(fmt.Sprintf("%d", index)).
			AddField(documents.NewKeywordField("name", fmt.Sprintf("hello-%d", index))).
			AddField(documents.NewKeywordField("index", fmt.Sprintf("%d", index))).
			AddField(documents.NewKeywordField("reversed-name", fmt.Sprintf("olleh-%d", index)))
		batch.Insert(doc)
	}
	b.ResetTimer()
	b.StartTimer()

	for b.Loop() {
		b.StopTimer()
		tmpIndexPath := testsuite.TemporaryDirectory(b)

		config := bluge.DefaultConfig(tmpIndexPath)
		writer, err := bluge.OpenOfflineWriter(config)
		if !assertions.NoError(err, "failed to open offline writer") {
			return
		}

		b.StartTimer()
		err = writer.Batch(batch)
		if err != nil {
			b.Fatal(err)
		}

		err = writer.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOfflineWriterWithDefinitions(b *testing.B) {
	assertions := assert.New(b)

	b.StopTimer()
	batch := index.NewBatch()
	for index := range WriterDocumentCount {
		info, fields := documents.FieldsFromDefinitions(
			documents.NewKeywordFieldDefinition("name", fmt.Sprintf("hello-%d", index)),
			documents.NewKeywordFieldDefinition("index", fmt.Sprintf("%d", index)),
			documents.NewKeywordFieldDefinition("reversed-name", fmt.Sprintf("olleh-%d", index)),
		)
		doc := documents.NewDocumentWithFields(
			fmt.Sprintf("%d", index),
			info, fields...,
		)
		batch.Insert(doc)
	}
	b.ResetTimer()
	b.StartTimer()

	for b.Loop() {
		b.StopTimer()
		tmpIndexPath := testsuite.TemporaryDirectory(b)

		config := bluge.DefaultConfig(tmpIndexPath)
		writer, err := bluge.OpenOfflineWriter(config)
		if !assertions.NoError(err, "failed to open offline writer") {
			return
		}

		b.StartTimer()
		err = writer.Batch(batch)
		if err != nil {
			b.Fatal(err)
		}

		err = writer.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOfflineWriterWithDefinitionsManagedId(b *testing.B) {
	assertions := assert.New(b)

	b.StopTimer()
	batch := index.NewBatch()
	for index := range WriterDocumentCount {
		info, fields := documents.FieldsFromDefinitionsWithId(
			fmt.Sprintf("%d", index),
			documents.NewKeywordFieldDefinition("name", fmt.Sprintf("hello-%d", index)),
			documents.NewKeywordFieldDefinition("index", fmt.Sprintf("%d", index)),
			documents.NewKeywordFieldDefinition("reversed-name", fmt.Sprintf("olleh-%d", index)),
		)
		doc := documents.NewDocumentWithFieldsManagedId(info, fields...)
		batch.Insert(doc)
	}
	b.ResetTimer()
	b.StartTimer()

	for b.Loop() {
		b.StopTimer()
		tmpIndexPath := testsuite.TemporaryDirectory(b)

		config := bluge.DefaultConfig(tmpIndexPath)
		writer, err := bluge.OpenOfflineWriter(config)
		if !assertions.NoError(err, "failed to open offline writer") {
			return
		}

		b.StartTimer()
		err = writer.Batch(batch)
		if err != nil {
			b.Fatal(err)
		}

		err = writer.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}
