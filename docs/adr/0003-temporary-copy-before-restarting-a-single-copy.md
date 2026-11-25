# 3. Make a temporary copy before restarting the only copy of a shard

**Decision.** When the node to restart holds the only serving copy of a shard, copy that shard to another node first, restart, then drop the extra copy.

**Why.** Replication factor 1 is common for cost reasons; Qdrant's own documentation says upgrades are zero-downtime only with 2 or more copies. The `no_surge` ablation shows 578 shard-minutes down instead of 64.

**Cost.** Temporary storage: about 12,700 GB-hours across the dev fleet's upgrades, shown in the report so it is a visible trade, not a hidden one.
