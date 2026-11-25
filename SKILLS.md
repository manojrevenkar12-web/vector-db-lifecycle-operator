# Repeatable tasks

## Add a safeguard
1. Add a field to `plan.Options`, on in `Safe()`, off in `AsIs()`.
2. Implement it in `internal/plan/plan.go` with a unit test in `plan_test.go`.
3. Add an ablation that switches it off in `config/designs.json` (`off` names the field).
4. `go run ./cmd/lab dev`; the "each safeguard matters" gate fails if the ablation is not worse on any measure.
5. If the operator should run it, add the step to `operator/internal/controller/reconciler.go` and a test.

## Add a fault to the world
1. Add the rate to `sim.Faults` and `config/world.json` (dev and heldout).
2. Draw it with `rng` keyed by seed, cluster, node and time only.
3. Add a test in `internal/sim/world_test.go`, including that the planner's actions cannot change it.

## Change what needs a person
Edit `Assess` in `internal/api/api.go`, add a case to `TestRiskyChangesNeedAPerson`, and update the README table.

## Regenerate reports
`go run ./cmd/lab dev` (never edit `REPORT.md`, the README block or `web/public/data/report.json` by hand).

## Run the operator end to end
See README, "Run it". CI does this on every push: `.github/workflows/ci.yml`, job `e2e`.

## Refresh screenshots
Start `go run ./cmd/api` and `cd web && npm run dev`, apply an upgrade to one cluster, then capture the live view, a refused risky change and the lab tab into `docs/screenshots/`.
