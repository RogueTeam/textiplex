package levenshtein

import (
	"iter"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/tidwall/btree"
)

// Levenshtein intersects a Levenshtein automaton (fixed target keyword, max
// edit distance K) with a byte-sorted tidwall BTreeG[[]byte].
//
// The tree MUST be ordered by bytes.Compare. The whole skip strategy relies on
// the automaton's byte ordering matching the tree's key ordering.
type Levenshtein struct {
	k       int
	m       int // max results; <=0 means unlimited
	keyword []byte
	tree    *btree.BTreeG[*storage.Token]
}

func New(k, m int, keyword []byte, tree *btree.BTreeG[*storage.Token]) *Levenshtein {
	if k < 0 || k > 3 || len(keyword) >= MaxLevenshteinLength {
		return nil
	}
	return &Levenshtein{k: k, m: m, keyword: keyword, tree: tree}
}

// State is the edit-distance DP row used as the automaton State:
//
//	State[i] = min edits to align the input consumed so far against keyword[:i]
//
// Values are clamped to k+1 ("too far"). The automaton consumes the *dictionary
// term* one byte at a time; the keyword is fixed. Each distinct row is a DFA
// State; we compute rows lazily instead of materializing the DFA.
type State []uint8

func (a *Levenshtein) cap() uint8 { return uint8(a.k + 1) }

// Start: the row for empty input, [0,1,2,...,n] clamped to k+1.
func (a *Levenshtein) Start() State {
	n := len(a.keyword)
	cap := a.cap()
	s := make(State, n+1)
	for i := 0; i <= n; i++ {
		if i <= a.k {
			s[i] = uint8(i)
		} else {
			s[i] = cap
		}
	}
	return s
}

// Step: transition on one input byte c.
func (a *Levenshtein) Step(prev State, c byte) State {
	n := len(a.keyword)
	cap := a.cap()
	next := make(State, n+1)

	if v := prev[0] + 1; v < cap { // delete input byte (insertion w.r.t. pattern)
		next[0] = v
	} else {
		next[0] = cap
	}
	for i := 1; i <= n; i++ {
		cost := uint8(1)
		if c == a.keyword[i-1] {
			cost = 0
		}
		v := prev[i-1] + cost // match / substitute
		if x := prev[i] + 1; x < v {
			v = x // delete input byte
		}
		if x := next[i-1] + 1; x < v {
			v = x // insert pattern byte
		}
		if v > cap {
			v = cap
		}
		next[i] = v
	}
	return next
}

func (a *Levenshtein) Accept(s State) bool { return s[len(s)-1] <= uint8(a.k) }

func (a *Levenshtein) Dead(s State) bool {
	for _, v := range s {
		if v <= uint8(a.k) {
			return false
		}
	}
	return true
}

// SmallestTransition: smallest byte b >= lb whose transition from s is not dead.
func (a *Levenshtein) SmallestTransition(s State, lb int) (byte, bool) {
	for x := lb; x <= 0xFF; x++ {
		if !a.Dead(a.Step(s, byte(x))) {
			return byte(x), true
		}
	}
	return 0, false
}

// NextSeek: smallest term strictly greater than key that the automaton can
// still follow. stack[i] is the (non-dead) state after consuming key[:i].
//
//   - matched < len(key): key[matched] dead-ended; need a byte > key[matched]
//     here, else backtrack to an earlier position.
//   - matched == len(key): key fully consumed; try to extend with any byte,
//     else backtrack.
//
// Walking deepest-to-shallowest yields the smallest valid successor (it shares
// the longest possible prefix with key), so no candidate term is ever skipped.
func (a *Levenshtein) NextSeek(stack []State, key []byte, matched int) []byte {
	for pos := matched; pos >= 0; pos-- {
		lb := 0
		if pos < len(key) {
			lb = int(key[pos]) + 1 // must strictly exceed the byte already tried/passed
		}
		if lb > 0xFF {
			continue
		}
		if b, ok := a.SmallestTransition(stack[pos], lb); ok {
			out := make([]byte, 0, pos+1)
			out = append(out, key[:pos]...)
			out = append(out, b)
			return out
		}
	}
	return nil
}

// Matches yields every term within edit distance k of the keyword, in ascending
// key order, capped at m. The yielded slice aliases the tree's stored key; copy
// it if you need to retain or mutate it.
func (a *Levenshtein) Matches() iter.Seq[*storage.Token] {
	return func(yield func(*storage.Token) bool) {
		count := 0
		var seek storage.Token // empty pivot => first key

		for {
			var key *storage.Token
			found := false
			a.tree.Ascend(&seek, func(item *storage.Token) bool {
				key, found = item, true
				return false // take only the first key >= seek
			})
			if !found {
				return
			}

			stack := make([]State, 1, len(key.Value)+1)
			stack[0] = a.Start()
			matched := 0
			for matched < len(key.Value) {
				ns := a.Step(stack[matched], key.Value[matched])
				if a.Dead(ns) {
					break
				}
				stack = append(stack, ns)
				matched++
			}

			if matched == len(key.Value) && a.Accept(stack[matched]) {
				if !yield(key) {
					return
				}
				if count++; a.m > 0 && count >= a.m {
					return
				}
			}

			next := a.NextSeek(stack, key.Value, matched)
			if next == nil {
				return
			}
			seek.Value = next
		}
	}
}
