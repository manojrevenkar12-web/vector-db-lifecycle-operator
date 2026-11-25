#!/usr/bin/env bash
# End-to-end on kind: provision, write data, rolling upgrade under reads, a crashed pod, scale-down with drain.
# Needs: a kind cluster in the current context with vdb-operator:dev loaded, kubectl, python3, jq.
set -euo pipefail
cd "$(dirname "$0")/.."
FROM=${FROM:-v1.15.0}
TO=${TO:-v1.16.0}
POINTS=${POINTS:-300}
OUT=${OUT:-e2e-result.json}
t0=$(date +%s)
log() { echo "[$(( $(date +%s) - t0 ))s] $*"; }

wait_phase() { # phase readyNodes timeout
  local deadline=$(( $(date +%s) + $3 ))
  while true; do
    local s; s=$(kubectl get vc vec -o json)
    local p r g o
    p=$(jq -r '.status.phase // ""' <<<"$s"); r=$(jq -r '.status.readyNodes // 0' <<<"$s")
    g=$(jq -r '.metadata.generation' <<<"$s"); o=$(jq -r '.status.observedGeneration // 0' <<<"$s")
    if [[ "$p" == "$1" && "$r" == "$2" && "$g" == "$o" ]]; then log "phase $p, $r ready"; return 0; fi
    if (( $(date +%s) > deadline )); then
      log "timeout waiting for $1/$2: phase=$p ready=$r"; kubectl get vc,pods,pvc -o wide; kubectl -n vdb-system logs deploy/vdb-operator --tail=80; return 1
    fi
    sleep 3
  done
}
db() { kubectl exec -i toolbox -- curl -sS -m 10 -H 'Content-Type: application/json' "$@"; }

kubectl apply -f config/crd/vectorclusters.yaml
kubectl apply -f config/rbac.yaml -f config/manager.yaml
kubectl -n vdb-system rollout status deploy/vdb-operator --timeout=120s
kubectl run toolbox --image=curlimages/curl:8.11.1 --restart=Never --command -- sleep 3600
kubectl wait --for=condition=Ready pod/toolbox --timeout=120s

log "provision 3 nodes at $FROM"
sed "s/v1.15.0/$FROM/" config/samples/cluster.yaml | kubectl apply -f -
wait_phase Healthy 3 420

log "create a collection with 6 shards x 2 replicas and write $POINTS points"
db -X PUT http://vec:6333/collections/e2e -d '{"vectors":{"size":4,"distance":"Dot"},"shard_number":6,"replication_factor":2}'
echo
python3 - "$POINTS" > /tmp/points.json <<'PY'
import json, sys
n = int(sys.argv[1])
print(json.dumps({"points": [{"id": i, "vector": [((i * k) % 7) / 7 for k in (1, 2, 3, 5)], "payload": {"n": i}} for i in range(n)]}))
PY
db -X PUT 'http://vec:6333/collections/e2e/points?wait=true' --data-binary @- < /tmp/points.json
echo
wait_phase Healthy 3 120

log "start a reader: an exact count every half second through the client Service"
cat > /tmp/reader.sh <<'SH'
rm -f /tmp/reads /tmp/stop
while [ ! -f /tmp/stop ]; do
  c=$(curl -s -m 3 -X POST -H 'Content-Type: application/json' http://vec:6333/collections/e2e/points/count -d '{"exact":true}' | sed -n 's/.*"count":\([0-9]*\).*/\1/p')
  echo "${c:-err}" >> /tmp/reads
  sleep 0.5
done
SH
kubectl exec -i toolbox -- sh -c 'cat > /tmp/reader.sh' < /tmp/reader.sh
kubectl exec toolbox -- sh -c 'nohup sh /tmp/reader.sh >/dev/null 2>&1 &'

log "rolling upgrade $FROM -> $TO"
up0=$(date +%s)
kubectl patch vc vec --type merge -p "{\"spec\":{\"version\":\"$TO\"}}"
sleep 5
wait_phase Healthy 3 900
up1=$(date +%s)
images=$(kubectl get pods -l lifecycle.vectordb.dev/cluster=vec -o jsonpath='{range .items[*]}{.spec.containers[0].image}{"\n"}{end}' | sort -u)
log "images now: $images"
[[ "$images" == "qdrant/qdrant:$TO" ]]

log "crash: delete pod vec-1 without warning"
kubectl delete pod vec-1 --wait=false
sleep 5
wait_phase Healthy 3 600

log "scale down to 2 nodes (drain first)"
kubectl patch vc vec --type merge -p '{"spec":{"nodes":2}}'
sleep 5
wait_phase Healthy 2 900
pods=$(kubectl get pods -l lifecycle.vectordb.dev/cluster=vec --no-headers | wc -l)
[[ "$pods" == "2" ]]

kubectl exec toolbox -- touch /tmp/stop
sleep 4
reads=$(kubectl exec toolbox -- cat /tmp/reads)
final=$(db -X POST http://vec:6333/collections/e2e/points/count -d '{"exact":true}' | jq '.result.count')
cluster=$(db http://vec:6333/collections/e2e/cluster)
copies=$(jq '[.result.local_shards[], .result.remote_shards[]] | map(select(.state=="Active")) | length' <<<"$cluster")
total=$(grep -c . <<<"$reads" || true)
ok=$(grep -c "^$POINTS\$" <<<"$reads" || true)
errs=$(grep -c '^err$' <<<"$reads" || true)
wrong=$(( total - ok - errs ))
jq -n --arg from "$FROM" --arg to "$TO" --argjson points "$POINTS" --argjson total "$total" --argjson ok "$ok" --argjson errs "$errs" \
  --argjson wrong "$wrong" --argjson final "$final" --argjson copies "$copies" --argjson upgrade_s $(( up1 - up0 )) \
  '{from:$from, to:$to, points:$points, reads:$total, readsCorrect:$ok, readErrors:$errs, readsWrongCount:$wrong, finalCount:$final, activeCopiesAfterScaleDown:$copies, upgradeSeconds:$upgrade_s}' | tee "$OUT"
kubectl get vc vec -o jsonpath='{.status.lastActions}'; echo

# A read may fail while a pod goes away; a read must never return a wrong count, and no point may be lost.
[[ "$wrong" == "0" ]] || { echo "a read returned a wrong count"; exit 1; }
[[ "$final" == "$POINTS" ]] || { echo "points lost: $final of $POINTS"; exit 1; }
[[ "$copies" == "12" ]] || { echo "want 6 shards x 2 active copies, got $copies"; exit 1; }
(( errs * 10 <= total )) || { echo "too many failed reads: $errs of $total"; exit 1; }
log "e2e passed"
