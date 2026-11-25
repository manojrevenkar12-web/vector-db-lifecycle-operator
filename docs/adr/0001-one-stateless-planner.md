# 1. One stateless planner for the lab, the operator and the API

**Decision.** All lifecycle decisions come from `plan.Plan(spec, observed, options)`: no I/O, no memory between calls. The operator and the simulator only observe and carry out steps.

**Why.** An operator that keeps its own record of "where it is in an upgrade" is wrong the moment it restarts or the database changes underneath it. Re-reading the world each time and taking the next safe step is how controllers converge. It also means the lab measures exactly the code that runs on Kubernetes.

**Cost.** Every look re-reads every collection's cluster info. Fine at tens of nodes; a large fleet needs caching of the observation, not of decisions.
