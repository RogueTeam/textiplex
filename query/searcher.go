package query

import (
	"github.com/RogueTeam/textiplex/storage"
)

type Searcher struct {
	Storage *storage.Storage
}

func New(s *storage.Storage) (searcher *Searcher) {
	searcher = &Searcher{
		Storage: s,
	}
	return searcher
}
