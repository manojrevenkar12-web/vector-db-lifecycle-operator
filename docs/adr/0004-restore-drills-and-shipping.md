# 4. A backup counts only after a restore drill, and the newest trusted one leaves the region

**Decision.** Restores and DR use the newest backup that was restored once in a drill. That backup is shipped to the DR region.

**Why.** Snapshots that were never restored fail when needed (8% are unrestorable in the dev world). Without the drill, region recovery takes longer on average because the first attempt may hit a bad backup (`no_verify`: RTO 1.68 h against 1.50 h). Without shipping, no DR cluster recovers at all (`no_ship`).

**Status.** Planned and measured in the lab. The Kubernetes adapter does not run backups yet (README, Limits).
