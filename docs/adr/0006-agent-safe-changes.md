# 6. Agents change clusters through preview, approval and idempotent apply

**Decision.** A change is previewed first; the preview names its effects and whether a person must approve it. Apply needs an `Idempotency-Key`, is refused if the cluster moved on since the preview (409), and an agent cannot approve or apply an unapproved risky change (403).

**Why.** Teams already drive managed databases from agents and MCP servers. The failure to design against is an agent retrying a scale-down or a downgrade it should not have made. Preview gives the agent and the person the same picture; the key makes retries harmless; the generation check stops applying a plan made against a cluster that has changed.
