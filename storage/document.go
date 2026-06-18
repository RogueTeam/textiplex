package storage

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
