package forkchoice

import (
	"errors"
	"math/rand"
	"testing"
)

func root(b byte) Root { var r Root; r[0] = b; return r }

// tree builds:
//
//	     0
//	   /   \
//	  1     2
//	 / \     \
//	3   4     5
func tree(t *testing.T) *Store {
	t.Helper()
	s := NewStore(Block{Root: root(0)})
	for _, b := range []Block{
		{root(1), root(0), 1}, {root(2), root(0), 1},
		{root(3), root(1), 2}, {root(4), root(1), 2}, {root(5), root(2), 2},
	} {
		if err := s.AddBlock(b); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func mustHead(t *testing.T, s *Store, from Root) Root {
	t.Helper()
	h, err := s.Head(from)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestNoVotesBreaksTiesByRoot(t *testing.T) {
	s := tree(t)
	// No weight anywhere: larger root wins at each step, 0 -> 2 -> 5.
	if h := mustHead(t, s, root(0)); h != root(5) {
		t.Fatalf("head = %x, want 05", h[0])
	}
}

func TestHeaviestSubtreeWins(t *testing.T) {
	s := tree(t)
	// Branch 1 gets 3 stake split across its children; branch 2 gets 2.
	_ = s.ProcessVote(1, root(3), 1, 1)
	_ = s.ProcessVote(2, root(4), 1, 2)
	_ = s.ProcessVote(3, root(5), 1, 2)
	if h := mustHead(t, s, root(0)); h != root(4) {
		t.Fatalf("head = %x, want 04", h[0])
	}
	if w, _ := s.Weight(root(1)); w != 3 {
		t.Fatalf("weight(1) = %d, want 3", w)
	}
}

func TestLatestMessageReplacesOlderVote(t *testing.T) {
	s := tree(t)
	_ = s.ProcessVote(1, root(3), 1, 10)
	if h := mustHead(t, s, root(0)); h != root(3) {
		t.Fatalf("head = %x, want 03", h[0])
	}
	_ = s.ProcessVote(1, root(5), 2, 10) // newer epoch moves the vote
	if h := mustHead(t, s, root(0)); h != root(5) {
		t.Fatalf("head = %x, want 05", h[0])
	}
	_ = s.ProcessVote(1, root(3), 2, 10) // same epoch is ignored
	_ = s.ProcessVote(1, root(3), 1, 10) // older epoch is ignored
	if h := mustHead(t, s, root(0)); h != root(5) {
		t.Fatalf("head = %x, want 05 after stale votes", h[0])
	}
}

func TestHeadFromJustifiedIgnoresOtherBranches(t *testing.T) {
	s := tree(t)
	_ = s.ProcessVote(1, root(5), 1, 100)
	if h := mustHead(t, s, root(1)); h != root(4) {
		t.Fatalf("head from 1 = %x, want 04", h[0])
	}
}

func TestAddBlockErrors(t *testing.T) {
	s := tree(t)
	if err := s.AddBlock(Block{root(9), root(8), 3}); !errors.Is(err, ErrUnknownParent) {
		t.Fatalf("err = %v, want ErrUnknownParent", err)
	}
	if err := s.AddBlock(Block{root(9), root(3), 2}); !errors.Is(err, ErrSlotNotAfterParent) {
		t.Fatalf("err = %v, want ErrSlotNotAfterParent", err)
	}
	if err := s.AddBlock(Block{root(3), root(1), 2}); err != nil {
		t.Fatalf("re-adding a known block: %v", err)
	}
	if s.Len() != 6 {
		t.Fatalf("len = %d, want 6", s.Len())
	}
}

func TestUnknownRoots(t *testing.T) {
	s := tree(t)
	if err := s.ProcessVote(1, root(9), 1, 1); !errors.Is(err, ErrUnknownBlock) {
		t.Fatalf("vote err = %v", err)
	}
	if _, err := s.Head(root(9)); !errors.Is(err, ErrUnknownBlock) {
		t.Fatalf("head err = %v", err)
	}
	if _, err := s.Weight(root(9)); !errors.Is(err, ErrUnknownBlock) {
		t.Fatalf("weight err = %v", err)
	}
	if err := s.Prune(root(9)); !errors.Is(err, ErrUnknownBlock) {
		t.Fatalf("prune err = %v", err)
	}
}

func TestPruneDropsOtherBranchesAndTheirVotes(t *testing.T) {
	s := tree(t)
	_ = s.ProcessVote(1, root(5), 1, 100)
	_ = s.ProcessVote(2, root(3), 1, 1)
	if err := s.Prune(root(1)); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 3 {
		t.Fatalf("len = %d, want 3", s.Len())
	}
	if h := mustHead(t, s, root(1)); h != root(3) {
		t.Fatalf("head = %x, want 03", h[0])
	}
	if err := s.AddBlock(Block{root(6), root(2), 3}); !errors.Is(err, ErrUnknownParent) {
		t.Fatalf("pruned parent still accepted: %v", err)
	}
}

// referenceHead is a deliberately naive second implementation: it recomputes
// every subtree weight by walking the whole tree from scratch.
func referenceHead(s *Store, justified Root) Root {
	var weight func(Root) uint64
	weight = func(r Root) uint64 {
		var w uint64
		for _, v := range s.votes {
			if v.root == r {
				w += v.balance
			}
		}
		for _, c := range s.nodes[r].children {
			w += weight(c)
		}
		return w
	}
	head := justified
	for len(s.nodes[head].children) > 0 {
		best := s.nodes[head].children[0]
		for _, c := range s.nodes[head].children[1:] {
			wc, wb := weight(c), weight(best)
			if wc > wb || (wc == wb && string(c[:]) > string(best[:])) {
				best = c
			}
		}
		head = best
	}
	return head
}

func randomStore(rng *rand.Rand, blocks, validators int) (*Store, []Root) {
	s := NewStore(Block{Root: root(0)})
	roots := []Root{root(0)}
	for i := 1; i < blocks; i++ {
		parent := roots[rng.Intn(len(roots))]
		var r Root
		rng.Read(r[:])
		_ = s.AddBlock(Block{Root: r, Parent: parent, Slot: s.nodes[parent].block.Slot + 1 + uint64(rng.Intn(3))})
		roots = append(roots, r)
	}
	for v := 0; v < validators; v++ {
		for e := 0; e < 1+rng.Intn(3); e++ {
			_ = s.ProcessVote(ValidatorIndex(v), roots[rng.Intn(len(roots))], uint64(rng.Intn(5)), uint64(1+rng.Intn(32)))
		}
	}
	return s, roots
}

func TestHeadMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		s, roots := randomStore(rng, 2+rng.Intn(40), rng.Intn(30))
		from := roots[rng.Intn(len(roots))]
		if got, want := mustHead(t, s, from), referenceHead(s, from); got != want {
			t.Fatalf("case %d: head %x, reference %x", i, got[:4], want[:4])
		}
	}
}

func FuzzHeadMatchesReference(f *testing.F) {
	f.Add(int64(1), uint8(10), uint8(5))
	f.Add(int64(42), uint8(60), uint8(40))
	f.Fuzz(func(t *testing.T, seed int64, blocks, validators uint8) {
		rng := rand.New(rand.NewSource(seed))
		s, roots := randomStore(rng, 2+int(blocks)%80, int(validators)%64)
		from := roots[rng.Intn(len(roots))]
		if got, want := mustHead(t, s, from), referenceHead(s, from); got != want {
			t.Fatalf("head %x, reference %x", got[:4], want[:4])
		}
	})
}

func BenchmarkHead(b *testing.B) {
	rng := rand.New(rand.NewSource(7))
	s, _ := randomStore(rng, 2000, 5000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Head(root(0)); err != nil {
			b.Fatal(err)
		}
	}
}
