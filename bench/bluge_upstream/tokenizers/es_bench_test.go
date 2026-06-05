package tokenizers_test

import (
	"testing"

	"github.com/blugelabs/bluge/analysis"
	"github.com/blugelabs/bluge/analysis/lang/es"
	"github.com/blugelabs/bluge/analysis/token"
	"github.com/blugelabs/bluge/analysis/tokenizer"
)

// same text as the textiplex Spanish tokenizer benchmark for a direct comparison
var spanishText = []byte(
	"El municipio de Bucaramanga suscribió un contrato de interventoría técnica " +
		"con la empresa Constructora Nacional para la construcción de la nueva sede " +
		"administrativa municipal. La contratista presentó su propuesta técnica y " +
		"económica ante el comité evaluador designado. Los funcionarios verificaron " +
		"que los documentos cumplían con todos los requisitos del pliego de condiciones. " +
		"El valor total del contrato ascendió a quinientos millones de pesos colombianos " +
		"y su vigencia fue de doce meses. La supervisión quedó a cargo del director de " +
		"obras públicas. El contrato fue publicado en el sistema SECOP según lo " +
		"establecido por la ley colombiana vigente.",
)

// analyzerNoStop mirrors BenchmarkSpanishTokenizer: no stop word filter, just
// tokenize + lowercase + stem.
var analyzerNoStop = &analysis.Analyzer{
	Tokenizer: tokenizer.NewUnicodeTokenizer(),
	TokenFilters: []analysis.TokenFilter{
		token.NewLowerCaseFilter(),
		es.StemmerFilter(),
	},
}

var sink int

func BenchmarkBlugeSpanishAnalyzer(b *testing.B) {
	b.SetBytes(int64(len(spanishText)))
	b.ReportAllocs()
	for b.Loop() {
		for _, tok := range es.Analyzer().Analyze(spanishText) {
			sink += len(tok.Term)
		}
	}
}

func BenchmarkBlugeSpanishAnalyzerWithoutStopwords(b *testing.B) {
	b.SetBytes(int64(len(spanishText)))
	b.ReportAllocs()
	for b.Loop() {
		for _, tok := range analyzerNoStop.Analyze(spanishText) {
			sink += len(tok.Term)
		}
	}
}
