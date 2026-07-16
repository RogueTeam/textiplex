package storage

import (
	"github.com/RogueTeam/textiplex/fastlog"
)

// IDF returns the Inverse Document Frequency for a single term.
// It answers: "how surprising is it to see this term in a document?"
// Document Count is the total number of documents indexed
// Token Document Frequency is how many documents contains the supplied token at least once
func InverseDocumentFrequency(docCount, tokenDocFreq uint64) float32 {
	return fastlog.Ln(1.0 + (float32(docCount-tokenDocFreq)+0.5)/(float32(tokenDocFreq)+0.5))
}

const (
	DefaultSaturation    float32 = 1.2
	DefaultLengthPenalty float32 = 0.75
)

// NormalizedTF returns the saturated, length-normalized term frequency
// for one term in one document's field.
// tokenFreq      - raw count: how many times the term appears in this doc's field
// documentLength - document length: number of tokens in this doc's field
// avgDocLength   - average document length across all docs for this field
// saturation     - saturation: how fast extra occurrences stop mattering (typically 1.2)
// lengthPenalty  - length penalty: how hard to punish long documents (typically 0.75)
func NormalizedTF(tokenFreq, documentLength uint32, avgDocLength float32) (normTf float32) {

	const (
		oneMinusLP     = 1 - DefaultLengthPenalty
		satXOneMinuxLp = DefaultSaturation * oneMinusLP
	)

	tf := float32(tokenFreq)
	dl := float32(documentLength)
	saturationXLengthPenaltyDivAvgDocLength := DefaultSaturation * (DefaultLengthPenalty / avgDocLength)
	denominator1 := tf + satXOneMinuxLp
	denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
	return tf / (denominator1 + denominator2)
}

func ScoreTermBM25(docCount, tokenDocFreq uint64, tokenFreq, documentLength uint32, avgDocLength float32) (score float32) {
	return InverseDocumentFrequency(docCount, tokenDocFreq) *
		NormalizedTF(tokenFreq, documentLength, avgDocLength)
}
