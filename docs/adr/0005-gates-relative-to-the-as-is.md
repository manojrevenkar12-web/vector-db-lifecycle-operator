# 5. Loss and downtime gates are ratios to the as-is

**Decision.** The gate on shards losing every copy (on clusters with 2+ copies) and the gate on downtime caused by the operator's own changes compare with the as-is design on the same fleet: at most 10% of it. Shards still lost at the end must be zero.

**Why.** Two disks can fail close together in the simulated world; no operator prevents that, so an absolute zero gate would test the dice, not the design. What the operator controls is whether the data comes back (zero lost at the end) and how much harm it causes itself.
