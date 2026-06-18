package es_test

import (
	"testing"

	"github.com/RogueTeam/textiplex/tokenizer/es"
)

// ~100 words of realistic Colombian public-procurement text. The mix of stop
// words, accented characters, and domain vocabulary exercises all three code
// paths: borrowed sub-slice, accent-fold allocation, and mente-strip.
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

// sink prevents the compiler from eliminating the benchmark loop body.
var sink int

func BenchmarkSpanishTokenizer(b *testing.B) {
	b.SetBytes(int64(len(spanishText)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for tok := range es.Tokenizer(spanishText) {
			sink += len(tok.Value)
		}
	}
}

func BenchmarkSpanishTokenizerWithoutStopwords(b *testing.B) {
	b.SetBytes(int64(len(spanishText)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for tok := range es.TokenizerWithoutStopwords(spanishText) {
			sink += len(tok.Value)
		}
	}
}
