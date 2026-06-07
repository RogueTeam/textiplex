package query

type SimpleQuery struct {
	Shoulds Clause
	Musts   Clause
	// Must not will not make use of boost
	MustNots Clause
}
