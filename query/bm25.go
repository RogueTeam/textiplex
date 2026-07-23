package query

import (
	"github.com/RogueTeam/textiplex/fastlog"
)

const (
	DefaultSaturation    = 1.2
	DefaultLengthPenalty = 0.75
)

// IDF returns the Inverse Document Frequency for a single term.
// It answers: "how surprising is it to see this term in a document?"
// Document Count is the total number of documents indexed
// Token Document Frequency is how many documents contains the supplied token at least once
func InverseDocumentFrequency(docCount, tokenDocFreq uint64) float32 {
	if tokenDocFreq > docCount || docCount == 0 {
		return 0
	}
	return fastlog.Ln(1.0 + (float32(docCount-tokenDocFreq)+0.5)/(float32(tokenDocFreq)+0.5))
}
