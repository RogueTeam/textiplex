package en

import "github.com/RogueTeam/textiplex/tokenizer"

var Stopwords = tokenizer.BuildStopWords(
	// articles
	"a", "an", "the",

	// prepositions
	"about", "above", "across", "after", "against",
	"along", "among", "around", "at",
	"before", "behind", "below", "between", "beyond",
	"by", "down",
	"dur", // during → dur (ing-strip)
	"except", "for", "from",
	"in", "into", "near", "of", "off", "on", "onto", "out",
	"over", "through", "to", "toward",
	"under", "until", "up", "upon",
	"with", "within", "without",

	// conjunctions and subordinators
	"and", "as", "because", "but",
	"either", "if", "neither", "nor", "or",
	"since", "so", "than", "that", "though",
	"unless", "when", "while", "yet",

	// pronouns
	"he", "her", "him",
	"hi", // his → hi (bare-s strip)
	"i",
	"it", // its → it (bare-s strip)
	"me", "my", "our", "she",
	"their", "them", "they",
	"us", "we", "who", "you", "your",

	// auxiliary and modal verbs
	"am", "are",
	"be", // being → be (ing-strip)
	"been", "can", "could", "did",
	"do", // does → do (es-strip), doing → do (ing-strip)
	"done", "get", "got", "had",
	"ha", // has → ha (bare-s strip)
	"have",
	"hav", // having → hav (ing-strip)
	"is", "let", "may", "might", "must",
	"need", "shall", "should",
	"wa", // was → wa (bare-s strip)
	"were", "will", "would",

	// determiners and quantifiers
	"all", "also", "any", "both", "each",
	"even", "every", "few", "many", "more",
	"most", "much", "no", "not", "other",
	"own", "same", "some", "such",

	// adverbs and discourse particles
	"again", "already", "also", "back", "far",
	"further", "here", "how", "just", "never",
	"now", "off", "often", "only", "quite",
	"rather", "really", "still", "then",
	"ther", // there  → ther (es-strip)
	"thi",  // this   → thi  (bare-s strip)
	"tho",  // those  → tho  (es-strip)
	"thu",  // thus   → thu  (bare-s strip)
	"too", "very", "well",
	"what", "when",
	"wher", // where  → wher (es-strip)
	"which", "who", "why",
)
