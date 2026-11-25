# 2. Gate the next restart on replicas being Active, not pods being Ready

**Decision.** During an upgrade the next node restarts only when no replica is Partial or Initializing, not when the restarted pod passes its readiness probe.

**Why.** A pod can be Ready while its replicas are still catching up. Restarting the next node then can leave a shard with no Active copy. The `no_wait_active` ablation shows the cost: unavailability caused by the operator's own changes goes from 64 to 163 shard-minutes in the dev fleet.

**Cost.** Upgrades take longer (mean change time 70 minutes against 10 for the as-is).
