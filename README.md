# ghost-forkchoice

A small, dependency-free Go implementation of **LMD-GHOST** fork choice, the rule Ethereum's consensus layer uses to pick the head of the chain.

Starting at the justified block, it repeatedly moves to the child whose subtree carries the most stake from each validator's **latest** vote. Ties go to the lexicographically larger block root, as in the consensus spec's `get_head`.

```go
s := forkchoice.NewStore(forkchoice.Block{Root: genesis})
_ = s.AddBlock(forkchoice.Block{Root: a, Parent: genesis, Slot: 1})
_ = s.AddBlock(forkchoice.Block{Root: b, Parent: genesis, Slot: 1})

_ = s.ProcessVote(7, a, 1, 32)  // validator 7, epoch 1, 32 stake
_ = s.ProcessVote(7, b, 2, 32)  // newer epoch replaces the old vote

head, _ := s.Head(genesis)       // b
```

## API

| Method | What it does |
|---|---|
| `NewStore(anchor)` | Tree rooted at genesis or the latest finalized block |
| `AddBlock(b)` | Insert a block; rejects unknown parents and non-increasing slots; idempotent |
| `ProcessVote(v, root, epoch, balance)` | Record a vote; only a strictly newer epoch replaces a validator's previous one |
| `Head(justified)` | Fork-choice head reachable from `justified` |
| `Weight(root)` | Total latest-vote stake on `root` and its descendants |
| `Prune(finalized)` | Drop blocks and votes outside the finalized subtree |

## Testing

```bash
go test -race -cover ./...                                   # unit tests, 100% statement coverage
go test -run=^$ -fuzz=FuzzHeadMatchesReference -fuzztime=30s  # differential fuzzing
go test -run=^$ -bench=. -benchmem                            # Head() on 2,000 blocks / 5,000 validators
```

The fuzz and randomized tests compare `Head` against a second, deliberately naive implementation that recomputes every subtree weight from scratch. On an Apple M-series laptop a 20-second fuzz run executed about 3.8 million cases with no mismatch, and `Head` took about 1.3 ms on the benchmark tree.

## Scope and limits

This is a reference implementation for reading and testing the rule, not a consensus client component. It leaves out on purpose:

- proposer boost,
- equivocating-validator handling,
- filtering branches by justified and finalized checkpoints,
- an incremental (proto-array) weight index: weights are recomputed per `Head` call in O(votes x depth).

The `Store` is not safe for concurrent use.

Written from the public Ethereum consensus specification. It contains no code from any employer or client project.

## License

MIT
