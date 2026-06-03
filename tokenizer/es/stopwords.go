package es

import "github.com/RogueTeam/textiplex/tokenizer"

var Stopwords = tokenizer.BuildStopWords(
	// articles and determiners
	"el", "la", "lo", "los", "las",
	"un", "una",
	"al",  // a + el contraction
	"del", // de + el contraction

	// personal and reflexive pronouns
	"yo", "tu", "el", "ella", "ello",
	"nos", "os", "se", "me", "te", "le", "les",

	// possessives (post-tokenizer; los/las forms already reduce via bare-s)
	"mi", "mis", "su", "sus", "tus",

	// prepositions
	"a", "ante", "bajo", "con", "contra",
	"de", "desde", "durante", "en", "entre",
	"hacia", "hasta", "mediante", "para", "por",
	"segun", // según → segun after fold
	"sin", "sobre",

	// conjunctions and subordinators
	"y", "e", "o", "u", "ni",
	"pero", "sino", "aunque", "como", "porque",
	"que", "si", "cuando", "donde", "mientras",
	"cual", // cuál → cual; cuales → cual via es-strip

	// demonstratives (post-tokenizer; estos→esto, estas→esta via bare-s)
	"este", "esta", "esto", "ese", "esa",

	// relative / interrogative (folded forms)
	"cuyo", "cuya",
	"aqui", // aquí → aqui
	"asi",  // así  → asi
	"mas",  // mas (unaccented); accented más→ma is too short to be useful

	// common verb forms
	"es", "son", "era", "fue",
	"ha", "han", "hay",
	"ser",

	// high-frequency adverbs and discourse particles
	"no", "ya", "muy", "bien", "solo",
	"todo", "toda",
	"otro",
	"cada", "tal",
	"si", // both conditional and affirmative; low signal either way
)
