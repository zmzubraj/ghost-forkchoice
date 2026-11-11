// Package forkchoice implements LMD-GHOST (Latest Message Driven, Greediest
// Heaviest Observed SubTree) fork choice over a block tree.
//
// The rule follows the Ethereum consensus specification's get_head: start at
// the justified root, and at every step move to the child whose subtree carries
// the most stake from validators' latest votes. Ties are broken by the
// lexicographically larger block root.
//
// Out of scope on purpose: proposer boost, equivocation handling, and
// filtering of branches by justified/finalized checkpoints. The package keeps
// the core rule small enough to read in one sitting and test exhaustively.
package forkchoice

import (
	"bytes"
	"errors"
	"fmt"
)

// Root identifies a block.
type Root [32]byte

// ValidatorIndex identifies a voting validator.
type ValidatorIndex uint64

// Block is the minimal block header fork choice needs.
type Block struct {
	Root   Root
	Parent Root
	Slot   uint64
}

var (
	// ErrUnknownParent is returned when a block's parent is not in the store.
	ErrUnknownParent = errors.New("forkchoice: unknown parent")
	// ErrUnknownBlock is returned when a root is not in the store.
	ErrUnknownBlock = errors.New("forkchoice: unknown block")
	// ErrSlotNotAfterParent is returned when a block's slot does not exceed its parent's.
	ErrSlotNotAfterParent = errors.New("forkchoice: slot must be greater than parent slot")
)

type node struct {
	block    Block
	children []Root
}

type vote struct {
	root    Root
	epoch   uint64
	balance uint64
}

// Store holds the block tree and each validator's latest vote.
// It is not safe for concurrent use; callers serialise access.
type Store struct {
	anchor Root
	nodes  map[Root]*node
	votes  map[ValidatorIndex]vote
}

// NewStore creates a store rooted at the given anchor block (for example the
// genesis block or the latest finalized block).
func NewStore(anchor Block) *Store {
	return &Store{
		anchor: anchor.Root,
		nodes:  map[Root]*node{anchor.Root: {block: anchor}},
		votes:  map[ValidatorIndex]vote{},
	}
}

// AddBlock inserts a block whose parent is already known. Re-adding a known
// block is a no-op.
func (s *Store) AddBlock(b Block) error {
	if _, ok := s.nodes[b.Root]; ok {
		return nil
	}
	parent, ok := s.nodes[b.Parent]
	if !ok {
		return fmt.Errorf("%w: %x", ErrUnknownParent, b.Parent[:4])
	}
	if b.Slot <= parent.block.Slot {
		return fmt.Errorf("%w: slot %d, parent slot %d", ErrSlotNotAfterParent, b.Slot, parent.block.Slot)
	}
	s.nodes[b.Root] = &node{block: b}
	parent.children = append(parent.children, b.Root)
	return nil
}

// ProcessVote records a validator's vote for root at epoch with the given
// effective balance. Only a strictly newer epoch replaces an earlier vote,
// which is what makes the rule "latest message driven".
func (s *Store) ProcessVote(v ValidatorIndex, root Root, epoch, balance uint64) error {
	if _, ok := s.nodes[root]; !ok {
		return fmt.Errorf("%w: %x", ErrUnknownBlock, root[:4])
	}
	if prev, ok := s.votes[v]; ok && epoch <= prev.epoch {
		return nil
	}
	s.votes[v] = vote{root: root, epoch: epoch, balance: balance}
	return nil
}

// Head returns the fork-choice head, walking down from justified.
func (s *Store) Head(justified Root) (Root, error) {
	if _, ok := s.nodes[justified]; !ok {
		return Root{}, fmt.Errorf("%w: %x", ErrUnknownBlock, justified[:4])
	}
	weights := s.subtreeWeights()
	head := justified
	for {
		children := s.nodes[head].children
		if len(children) == 0 {
			return head, nil
		}
		best := children[0]
		for _, c := range children[1:] {
			if heavier(c, best, weights) {
				best = c
			}
		}
		head = best
	}
}

// Weight returns the total stake of latest votes for root and its descendants.
func (s *Store) Weight(root Root) (uint64, error) {
	if _, ok := s.nodes[root]; !ok {
		return 0, fmt.Errorf("%w: %x", ErrUnknownBlock, root[:4])
	}
	return s.subtreeWeights()[root], nil
}

// Prune drops every block that does not descend from newAnchor (typically the
// newly finalized block) and every vote that points at a dropped block.
func (s *Store) Prune(newAnchor Root) error {
	if _, ok := s.nodes[newAnchor]; !ok {
		return fmt.Errorf("%w: %x", ErrUnknownBlock, newAnchor[:4])
	}
	keep := map[Root]*node{}
	stack := []Root{newAnchor}
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		keep[r] = s.nodes[r]
		stack = append(stack, s.nodes[r].children...)
	}
	for v, vt := range s.votes {
		if _, ok := keep[vt.root]; !ok {
			delete(s.votes, v)
		}
	}
	s.nodes = keep
	s.anchor = newAnchor
	return nil
}

// Len reports how many blocks the store holds.
func (s *Store) Len() int { return len(s.nodes) }

// subtreeWeights adds each vote's balance to the voted block and every
// ancestor up to the anchor. A post-order pass over the tree would be
// O(blocks); this version is O(votes x depth) and kept for clarity.
func (s *Store) subtreeWeights() map[Root]uint64 {
	w := make(map[Root]uint64, len(s.nodes))
	for _, vt := range s.votes {
		r := vt.root
		for {
			w[r] += vt.balance
			if r == s.anchor {
				break
			}
			r = s.nodes[r].block.Parent
		}
	}
	return w
}

// heavier reports whether a should win over b: more weight, or equal weight
// and the lexicographically larger root.
func heavier(a, b Root, w map[Root]uint64) bool {
	if w[a] != w[b] {
		return w[a] > w[b]
	}
	return bytes.Compare(a[:], b[:]) > 0
}
