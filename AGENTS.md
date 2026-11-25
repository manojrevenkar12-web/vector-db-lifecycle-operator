# Working on this repository

## Fast feedback

```bash
go vet ./... && go test ./...          # seconds
go run ./cmd/lab dev && go run ./cmd/lab gate dev   # about 20 s
cd web && npm test && npm run typecheck
cd operator && go test ./...           # needs module access
```

## Invariants

1. **The truth stays in the world.** Crash schedules, which disk is lost, which backup does not restore and which transfer fails live in `internal/sim` and are drawn from the seed. The planner only sees what a real operator would observe: nodes, replica states, transfers, backup records.
2. **The world does not read the operator's settings.** `internal/sim` never imports `internal/plan` or reads `designs.json`.
3. **Same luck for every design.** Every random draw is keyed by seed, cluster, node and time (`internal/rng`), never by what the planner did. A test checks that actions cannot move a crash.
4. **One planner.** The lab, the operator and the change API call the same `plan.Plan`. A safeguard added to one is added to all, and the lab measures it.
5. **One step at a time, and safe if repeated.** The planner is stateless: it re-reads the world on every look, so a crash of the operator mid-change loses nothing. Every step is safe to issue twice.
6. **A person approves risky changes.** An agent can preview and apply ordinary changes; it cannot approve, and it cannot apply a change that needs a person until one has approved it.
7. **Settings in config.** Fault rates, designs, seeds, gates: `config/*.json`. No tunable number in code.
8. **Generated reports.** `REPORT.md`, the README results block and `web/public/data/report.json` are written by `go run ./cmd/lab`; never edit them by hand. CI regenerates them and fails on a diff.

## The held-out rule

`go run ./cmd/lab heldout` runs the held-out fleet once and writes `reports/heldout.lock` before it starts. Do not delete the lock, change held-out seeds, or tune anything after looking at held-out numbers. New held-out seeds need a new name and an ADR.
