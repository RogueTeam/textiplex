package testsuite

import (
	"testing"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/stretchr/testify/assert"
)

// MakeToken creates a TokenDefinition with the given normalized value and frequency.
// The caller is responsible for normalization before passing the value.
func MakeToken[T ~string | []byte](value T, freq uint64) *storage.TokenDefinition {
	return &storage.TokenDefinition{Value: []byte(value), Frequency: freq}
}

// MakeField creates a FieldDefinition with the given xxh3 field hash, total token
// length for this document, and the list of token definitions.
func MakeField(hash uint64, length uint64, tokens ...*storage.TokenDefinition) *storage.FieldDefinition {
	return &storage.FieldDefinition{Hash: hash, Length: length, Tokens: tokens}
}

// MakeDoc creates a Document with the given external ID and field definitions.
// The ID must be unique across the index and will be sorted alphabetically
// during SortAndBuildFrom / BuildFrom.
func MakeDoc[T ~string | ~[]byte](id T, fields ...*storage.FieldDefinition) *storage.Document {
	return &storage.Document{Id: storage.DocumentId(id), Fields: fields}
}

// RoundTrip saves the storage to a buffer and loads it back into a fresh Storage.
// It returns the loaded storage. Any load error is returned to the caller.
func RoundTrip(tb testing.TB, s *storage.Storage) *storage.Storage {
	assertions := assert.New(tb)

	filename := TempFilename(tb, "roundtrip_*.bin")

	err := s.SaveTo(filename)
	if !assertions.NoError(err, "save into file") {
		return nil
	}

	loaded := &storage.Storage{}

	err = loaded.Load(filename)
	if !assertions.NoError(err, "failed to load file") {
		return nil
	}
	tb.Cleanup(func() {
		loaded.Close()
	})

	return loaded
}
