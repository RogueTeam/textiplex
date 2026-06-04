package fields

import (
	"unsafe"

	"github.com/RogueTeam/textiplex/storage"
)

func TokenSize(t *storage.TokenDefinition) (size uint64) {
	return uint64(unsafe.Sizeof(storage.TokenDefinition{})) + uint64(len(t.Value))
}

// Computes the base size of the struct such as the raw size and the length of pointers found
// Caller still requires to compute the size of the tokens in the field
func BaseFieldDefinitionSize(f *storage.FieldDefinition) (size uint64) {
	// Actual struct size
	return uint64(unsafe.Sizeof(storage.FieldDefinition{})) +
		// Size of all pointers referenced
		uint64(unsafe.Sizeof((*storage.FieldDefinition)(nil)))*uint64(len(f.Tokens))
}

func BaseDocumentSize(d *storage.Document) (size uint64) {
	size += uint64(unsafe.Sizeof(storage.Document{}))
	size += uint64(len(d.Id))

	// Fields: pointer array + struct bodies
	size += uint64(unsafe.Sizeof((*storage.FieldDefinition)(nil))) * uint64(len(d.Fields))
	return size
}
