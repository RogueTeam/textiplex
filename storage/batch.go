package storage

type Batch struct {
	// Read only field computed with each insertion
	// Size is computed from the references slices and other values present on all the documents
	// Useful as a hard limit to not grow the batch more than e.g: 5GB or so
	Size uint64
	// List of the documents already present on the batch
	Documents []*Document
}

func NewBatch(docs ...*Document) (batch *Batch) {
	batch = &Batch{
		Documents: docs,
	}
	for _, doc := range docs {
		batch.Size += doc.Size()
	}
	return batch
}

func (b *Batch) Reset() {
	b.Size = 0
	b.Documents = b.Documents[:0]
}

// Inserts a new document into the batch and updates the size of the batch
func (b *Batch) Insert(doc *Document) {
	b.Size += doc.Size()
	b.Documents = append(b.Documents, doc)
}
