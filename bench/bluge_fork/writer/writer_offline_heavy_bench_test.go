package writer_test

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/RogueTeam/textiplex/testsuite/wikipedia"
	"github.com/pluto-org-co/bluge"
	"github.com/pluto-org-co/bluge/documents"
	"github.com/pluto-org-co/bluge/index"
	"github.com/pluto-org-co/bluge/testsuite"
	"github.com/stretchr/testify/assert"
)

const MaxBatchSize = 50_000

func BenchmarkOfflineWriterHeavyWithDefinitionsManagedId(b *testing.B) {
	assertions := assert.New(b)

	b.StopTimer()

	batchPool := sync.Pool{
		New: func() any {
			return index.NewBatch()
		},
	}

	tmpIndexPath := testsuite.TemporaryDirectory(b)

	config := bluge.DefaultConfig(tmpIndexPath)
	writer, err := bluge.OpenOfflineWriter(config)
	if !assertions.NoError(err, "failed to open offline writer") {
		return
	}

	pages, err := wikipedia.Pages()
	if !assertions.NoError(err, "failed to prepare pages") {
		return
	}

	b.ResetTimer()
	b.StartTimer()

	workerCount := min(4, runtime.NumCPU())
	workers := make(chan struct{}, workerCount)
	for range workerCount {
		workers <- struct{}{}
	}

	batchCh := make(chan *index.Batch, workerCount)
	go func() {
		defer close(batchCh)

		var batchSize uint64

		batch := batchPool.Get().(*index.Batch)
		for page := range pages {
			info, fields := documents.FieldsFromDefinitionsWithId(
				fmt.Sprintf("%d", page.Id),
				documents.NewKeywordFieldDefinition("title", page.Title),
				documents.NewKeywordFieldDefinition("content", page.Content),
			)

			batch.Insert(documents.NewDocumentWithFieldsManagedId(info, fields...))
			batchSize++

			if batchSize >= MaxBatchSize {
				<-workers
				batchCh <- batch
				batch = batchPool.Get().(*index.Batch)
				batchSize = 0
			}
		}

	}()

	var errorsCh = make(chan error, workerCount)
	go func() {
		defer close(errorsCh)

		var wg sync.WaitGroup
		for batch := range batchCh {
			wg.Go(func() {
				defer func() {
					batchPool.Put(batch)
					workers <- struct{}{}
				}()

				err = writer.Batch(batch)
				if err != nil {
					errorsCh <- err
				}
			})
		}

		wg.Wait()
	}()

	var allErrors []error
	for err := range errorsCh {
		allErrors = append(allErrors, err)
	}

	if !assertions.Empty(allErrors, "expecting no errors, got: %v", errors.Join(allErrors...)) {
		return
	}

	err = writer.Close()
	if !assertions.NoError(err, "failed to merge and close writer") {
		return
	}
}
