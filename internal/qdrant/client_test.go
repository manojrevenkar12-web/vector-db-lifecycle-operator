package qdrant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
)

func fake(t *testing.T, calls *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*calls = append(*calls, r.Method+" "+r.URL.RequestURI()+" "+string(body))
		if r.Header.Get("api-key") != "k" {
			w.WriteHeader(401)
			return
		}
		res := map[string]any{}
		switch r.URL.Path {
		case "/cluster":
			res = map[string]any{"status": "enabled", "peer_id": 11}
		case "/collections":
			res = map[string]any{"collections": []map[string]any{{"name": "docs"}}}
		case "/collections/docs/cluster":
			if r.Method == http.MethodGet {
				res = map[string]any{
					"peer_id":         11,
					"local_shards":    []map[string]any{{"shard_id": 0, "state": "Active"}, {"shard_id": 1, "state": "Partial"}},
					"remote_shards":   []map[string]any{{"shard_id": 0, "peer_id": 22, "state": "Dead"}, {"shard_id": 1, "peer_id": 33, "state": "Active"}},
					"shard_transfers": []map[string]any{{"shard_id": 1, "from": 33, "to": 22}},
				}
			}
		case "/snapshots":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"name": "full-1.snapshot", "creation_time": "2026-11-25T10:00:00"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": res, "status": "ok"})
	}))
}

func TestReadsPeersShardsTransfersAndSnapshots(t *testing.T) {
	var calls []string
	s := fake(t, &calls)
	defer s.Close()
	c := New(s.URL, "k")
	ctx := context.Background()
	p, err := c.PeerID(ctx)
	if err != nil || p != 11 {
		t.Fatalf("peer %d %v", p, err)
	}
	shards, moves, err := c.Shards(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 2 || shards[0].Replicas[0] != (model.Replica{Peer: 11, State: model.Active}) || shards[0].Replicas[1].State != model.Dead {
		t.Fatalf("shards %+v", shards)
	}
	if shards[1].Replicas[0].State != model.Partial || len(moves) != 1 || moves[0].To != 22 {
		t.Fatalf("shard 1 %+v moves %+v", shards[1], moves)
	}
	now, _ := time.Parse(time.RFC3339, "2026-11-25T12:00:00Z")
	bs, err := c.Snapshots(ctx, now)
	if err != nil || len(bs) != 1 || bs[0].At != -120 {
		t.Fatalf("snapshots %+v %v", bs, err)
	}
}

func TestExecuteSendsTheDatabaseCalls(t *testing.T) {
	var calls []string
	s := fake(t, &calls)
	defer s.Close()
	c := New(s.URL, "k")
	ctx := context.Background()
	if err := c.Execute(ctx, model.Action{Kind: model.ReplicateShard, Collection: "docs", Shard: 1, From: 33, To: 22}); err != nil {
		t.Fatal(err)
	}
	if err := c.Execute(ctx, model.Action{Kind: model.DropReplica, Collection: "docs", Shard: 0, From: 22}); err != nil {
		t.Fatal(err)
	}
	if err := c.Execute(ctx, model.Action{Kind: model.RestartNode}); err == nil {
		t.Fatal("a restart is not a database call")
	}
	want := []string{
		`POST /collections/docs/cluster {"replicate_shard":{"from_peer_id":33,"shard_id":1,"to_peer_id":22}}`,
		`POST /collections/docs/cluster {"drop_replica":{"peer_id":22,"shard_id":0}}`,
	}
	for i, w := range want {
		if calls[i] != w {
			t.Fatalf("call %d: %s, want %s", i, calls[i], w)
		}
	}
}

func TestErrorsCarryTheStatus(t *testing.T) {
	var calls []string
	s := fake(t, &calls)
	defer s.Close()
	if _, err := New(s.URL, "wrong").PeerID(context.Background()); err == nil {
		t.Fatal("expected an error for a wrong key")
	}
}
