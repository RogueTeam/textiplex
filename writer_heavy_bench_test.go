package textiplex_test

import (
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/RogueTeam/textiplex"
	"github.com/RogueTeam/textiplex/fields"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/RogueTeam/textiplex/testsuite/wikipedia"
	"github.com/RogueTeam/textiplex/tokenizer/en"
	"github.com/stretchr/testify/assert"
)

const MaxBatchSize = 1024 * 1024 * 1024

func BenchmarkHeavyWriter(b *testing.B) {
	b.StopTimer()

	assertions := assert.New(b)

	var writer = textiplex.Writer{
		TemporaryDirectory: testsuite.TempDirectory(b, "temp-*"),
		Directory:          testsuite.TempDirectory(b, "segments-*"),
	}

	maxWorkers := min(4, runtime.NumCPU())

	batchPool := sync.Pool{
		New: func() any {
			return fields.NewBatch(1_000)
		},
	}

	var workers = make(chan struct{}, maxWorkers)
	for range maxWorkers {
		workers <- struct{}{}
	}

	pages, err := wikipedia.Pages()
	if !assertions.NoError(err, "failed to prepare pages reader") {
		return
	}

	b.ResetTimer()
	b.ReportAllocs()
	b.StartTimer()

	b.Run("Indexing", func(b *testing.B) {
		b.StopTimer()

		assertions := assert.New(b)

		b.ResetTimer()
		b.ReportAllocs()
		b.StartTimer()
		var batchsCh = make(chan *fields.Batch, maxWorkers)

		go func() {
			fieldsPool := pool.New[storage.FieldDefinition](200)
			tokenPool := pool.New[storage.TokenDefinition](200)

			var totalCount, batchCount uint64
			batch := batchPool.Get().(*fields.Batch)
			batch.Reset()

			for page := range pages {
				titleField := fieldsPool.Get()
				content := fieldsPool.Get()
				totalFieldSize := fields.TextField(titleField, tokenPool, "title", page.Title, en.Tokenizer)
				totalFieldSize += fields.TextField(content, tokenPool, "content", page.Content, en.Tokenizer)

				batch.Insert(strconv.AppendInt(nil, page.Id, 10), totalFieldSize, titleField, content)
				batchCount++
				totalCount++

				if batch.Size >= MaxBatchSize {
					b.Logf("Prepared batch - document count: %d - total: %d", batchCount, totalCount)
					<-workers
					batchsCh <- batch
					batch = batchPool.Get().(*fields.Batch)
					batch.Reset()
					batchCount = 0
				}
			}

			if batch.Size > 0 {
				b.Logf("Prepared final batch - document count: %d - total: %d", batchCount, totalCount)
				<-workers
				batchsCh <- batch
			}

			close(batchsCh)
		}()

		var errorsCh = make(chan error)
		go func() {
			var wg sync.WaitGroup

			for batch := range batchsCh {
				wg.Go(func() {
					defer func() { workers <- struct{}{} }()

					b.Logf("Inserting batch of size %d", batch.Size)
					err := writer.Batch(batch)
					if err != nil {
						errorsCh <- err
					}
				})
			}

			wg.Wait()
			close(errorsCh)
		}()

		var allErrors []error
		for err := range errorsCh {
			allErrors = append(allErrors, err)
		}

		if !assertions.Len(allErrors, 0, "expecting no errors") {
			return
		}
	})

	b.Run("Merge", func(b *testing.B) {
		b.StopTimer()

		assertions := assert.New(b)

		b.ResetTimer()
		b.ReportAllocs()
		b.StartTimer()

		err := writer.Merge()
		if !assertions.Nil(err, "failed to merge") {
			return
		}
	})
}
