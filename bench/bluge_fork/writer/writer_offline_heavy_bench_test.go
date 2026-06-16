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

const MaxBatchSize = 10_000

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

	// batchCh is the only backpressure mechanism: bounded to workerCount so the
	// producer cannot get more than workerCount batches ahead of the consumers.
	// The separate `workers` token channel is removed; it created a circular
	// wait whenever a worker blocked sending to errorsCh while still owing a
	// token to the producer.
	batchCh := make(chan *index.Batch, workerCount)

	// Producer: builds batches and hands them off. Bounded by batchCh capacity.
	go func() {
		defer close(batchCh)

		var totalProcessed, batchSize uint64
		batch := batchPool.Get().(*index.Batch)
		batch.Reset()

		for page := range pages {
			info, fields := documents.FieldsFromDefinitionsWithId(
				fmt.Sprintf("%d", page.Id),
				documents.NewKeywordFieldDefinition("title", page.Title),
				documents.NewKeywordFieldDefinition("content", page.Content),
			)
			batch.Insert(documents.NewDocumentWithFieldsManagedId(info, fields...))
			batchSize++
			totalProcessed++

			if totalProcessed%1_000_000 == 0 {
				b.Logf("Total processed: %d", totalProcessed)
			}

			if batchSize >= MaxBatchSize {

				b.Logf("Processing Batch: %d", batchSize)
				batchCh <- batch
				batch = batchPool.Get().(*index.Batch)
				batch.Reset()
				batchSize = 0
			}
		}

		if batchSize > 0 {
			batchCh <- batch
		} else {
			batchPool.Put(batch)
		}
	}()

	// errorsCh is buffered to hold one error per possible failing batch is not
	// feasible, so instead the consumer drains it concurrently below. We keep a
	// small buffer and guarantee the drain goroutine runs in parallel, so a
	// worker can never wedge on the send.
	errorsCh := make(chan error, workerCount)

	// Fan-out consumers with a fixed worker pool. Workers pull from batchCh,
	// so concurrency is naturally capped at workerCount without a token channel.
	var consumerWG sync.WaitGroup
	consumerWG.Add(workerCount)
	for range workerCount {
		go func() {
			defer consumerWG.Done()
			for batch := range batchCh {
				err := writer.Batch(batch)
				batchPool.Put(batch)
				if err != nil {
					errorsCh <- err
				}
			}
		}()
	}

	// Close errorsCh once all consumers are done.
	go func() {
		consumerWG.Wait()
		close(errorsCh)
	}()

	// Drain errors concurrently with the consumers; this is what prevents any
	// worker from blocking forever on `errorsCh <- err`.
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
