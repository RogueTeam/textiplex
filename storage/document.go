package storage

import "unsafe"

type TokenDefinition struct {
	// Normalized token value e.g. "interventoria" not "Interventoría"
	// The caller is responsible for normalization before insertion
	Value []byte
	// How many times this token appears in this field of this document
	Frequency uint64
}

type FieldDefinition struct {
	// xxh3 hash of the field name
	Hash uint64
	// Total number of tokens in this field for this document
	// Used to update avgdl and store as DocumentLengthEntry
	Length uint64
	// Tokens found in this field for this document
	// Caller must deduplicate — one entry per unique token
	// Frequency carries the count of occurrences
	Tokens []*TokenDefinition
}

type Document struct {
	// External document identifier e.g. "CO1.PCCNTR.123456"
	// Must be unique across the index
	// Will be inserted sorted into DocumentsIds
	Id DocumentId
	// Fields present in this document
	// Fields absent from this slice are treated as empty for this document
	Fields []*FieldDefinition
}

func (d *Document) Size() (size uint64) {
	size += uint64(unsafe.Sizeof(Document{}))
	size += uint64(unsafe.Sizeof((*Document)(nil))) // pointer in b.Documents
	size += uint64(len(d.Id))

	// Fields: pointer array + struct bodies
	size += uint64(unsafe.Sizeof((*FieldDefinition)(nil))) * uint64(len(d.Fields))
	size += uint64(unsafe.Sizeof(FieldDefinition{})) * uint64(len(d.Fields))

	for _, field := range d.Fields {
		// Tokens: pointer array + struct bodies
		size += uint64(unsafe.Sizeof((*TokenDefinition)(nil))) * uint64(len(field.Tokens))
		size += uint64(unsafe.Sizeof(TokenDefinition{})) * uint64(len(field.Tokens))
		for _, tok := range field.Tokens {
			size += uint64(len(tok.Value))
		}
	}
	return size
}
