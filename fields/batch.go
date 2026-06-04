package fields

import (
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
)

type Batch struct {
	DocumentsPool *pool.Pool[storage.Document]
	// Read only field computed with each insertion
	// Size is computed from the references slices and other values present on all the documents
	// Useful as a hard limit to not grow the batch more than e.g: 5GB or so
	Size uint64
	// List of the documents already present on the batch
	Documents []*storage.Document
}

// size if the size of bytes of all documents combined
// docs
func NewBatch(docsPoolSize int) (batch *Batch) {
	return &Batch{
		DocumentsPool: pool.New[storage.Document](docsPoolSize),
	}
}

func (b *Batch) Reset() {
	b.Size = 0
	b.Documents = b.Documents[:0]
}

// Inserts a new document into the batch and updates the size of the batch
func (b *Batch) Insert(id storage.DocumentId, totalFieldsSize uint64, fields ...*storage.FieldDefinition) {
	doc := b.DocumentsPool.Get()
	doc.Id = id
	doc.Fields = fields

	b.Size += totalFieldsSize + BaseDocumentSize(doc)
	b.Documents = append(b.Documents, doc)
}
