package query

import "github.com/RogueTeam/textiplex/fastlog"

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

const (
	DefaultSaturation    = 1.2
	DefaultLengthPenalty = 0.75
)

// NormalizedTF returns the saturated, length-normalized term frequency
// for one term in one document's field.
// tokenFreq      - raw count: how many times the term appears in this doc's field
// documentLength - document length: number of tokens in this doc's field
// avgDocLength   - average document length across all docs for this field
// saturation     - saturation: how fast extra occurrences stop mattering (typically 1.2)
// lengthPenalty  - length penalty: how hard to punish long documents (typically 0.75)
func NormalizedTF(tokenFreq, documentLength uint64, avgDocLength, saturation, lengthPenalty float32) (normTf float32) {
	tf := float32(tokenFreq)
	dl := float32(documentLength)

	// How much longer/shorter is this doc vs the average.
	// dl/avgDocLength == 1.0 for an average-length doc → no penalty.
	lengthRatio := dl / avgDocLength

	// The length normalization term.
	// lengthPenalty=0 → always 1.0, doc length is ignored entirely.
	// lengthPenalty=1 → full density normalization.
	lengthNorm := 1 - lengthPenalty + lengthPenalty*lengthRatio

	// Saturated TF. The ceiling as tf→∞ is (saturation+1)/saturation.
	// For saturation=1.2 that ceiling is 1.833 — tf=100 and tf=1000
	// produce nearly identical scores.
	return (tf * (saturation + 1)) / (tf + saturation*lengthNorm)
}

func ScoreTermBM25(docCount, tokenDocFreq, tokenFreq, documentLength uint64, avgDocLength, saturation, lengthPenalty float32) (score float32) {
	return InverseDocumentFrequency(docCount, tokenDocFreq) *
		NormalizedTF(tokenFreq, documentLength, avgDocLength, saturation, lengthPenalty)
}
