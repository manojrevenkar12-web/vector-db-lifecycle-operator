package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
)

type fake struct {
	spec model.Spec
	gen  int
	obs  model.Observed
}

func (f *fake) IDs() []string { return []string{"c1"} }
func (f *fake) Spec(id string) (model.Spec, int, bool) {
	return f.spec, f.gen, id == "c1"
}
func (f *fake) SetSpec(_ string, s model.Spec) int { f.spec = s; f.gen++; return f.gen }
func (f *fake) Observe(string) model.Observed      { return f.obs }
func (f *fake) Status(string) model.Decision       { return model.Decision{Phase: model.Healthy} }

func setup() (*fake, http.Handler) {
	f := &fake{spec: model.Spec{Version: "v1.15.5", Nodes: 3, ReplicationFactor: 2, DR: &model.DRSpec{Region: "eu-west"}}, gen: 1,
		obs: model.Observed{Nodes: []model.Node{{Name: "vdb-0", Peer: 1}, {Name: "vdb-1", Ordinal: 1, Peer: 2}, {Name: "vdb-2", Ordinal: 2, Peer: 3}},
			Shards: []model.Shard{{Collection: "c", ID: 0, Replicas: []model.Replica{{Peer: 3}}}}}}
	s := New(f)
	n := 0
	s.newID = func() string { n++; return "chg-" + string(rune('a'+n)) }
	return f, s.Handler()
}

func call(t *testing.T, h http.Handler, method, path, body string, hdr ...string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

const agent = `{"actor":{"kind":"agent","id":"ops-bot"}}`

func TestPreviewThenApplyAsAnAgent(t *testing.T) {
	f, h := setup()
	code, pv := call(t, h, "POST", "/v1/clusters/c1/changes/preview", `{"version":"v1.16.3"}`)
	if code != 201 {
		t.Fatalf("%d %v", code, pv)
	}
	eff := pv["effects"].(map[string]any)
	if eff["restarts"].(float64) != 3 || eff["temporaryCopies"].(float64) != 1 || eff["needsPerson"].(bool) {
		t.Fatalf("%v", eff)
	}
	id := pv["id"].(string)
	code, out := call(t, h, "POST", "/v1/changes/"+id+"/apply", agent, "Idempotency-Key", "k-00000001")
	if code != 200 || f.spec.Version != "v1.16.3" || out["generation"].(float64) != 2 {
		t.Fatalf("%d %v", code, out)
	}
	code, again := call(t, h, "POST", "/v1/changes/"+id+"/apply", agent, "Idempotency-Key", "k-00000001")
	if code != 200 || again["generation"] != out["generation"] || f.gen != 2 {
		t.Fatalf("replay must return the same result and change nothing: %d %v", code, again)
	}
	if code, _ := call(t, h, "POST", "/v1/changes/"+id+"/apply", agent, "Idempotency-Key", "k-00000002"); code != 409 {
		t.Fatalf("a preview applies once: %d", code)
	}
}

func TestRiskyChangesNeedAPerson(t *testing.T) {
	_, h := setup()
	for _, body := range []string{`{"replicationFactor":1}`, `{"dr":false}`, `{"version":"v1.17.1"}`, `{"version":"v1.14.0"}`, `{"nodes":1}`, `{"nodes":7}`} {
		code, pv := call(t, h, "POST", "/v1/clusters/c1/changes/preview", body)
		if code != 201 || !pv["effects"].(map[string]any)["needsPerson"].(bool) {
			t.Fatalf("%s: %d %v", body, code, pv)
		}
	}
	_, pv := call(t, h, "POST", "/v1/clusters/c1/changes/preview", `{"replicationFactor":1}`)
	id := pv["id"].(string)
	if code, _ := call(t, h, "POST", "/v1/changes/"+id+"/apply", agent, "Idempotency-Key", "k-00000003"); code != 403 {
		t.Fatalf("agent alone: %d", code)
	}
	if code, _ := call(t, h, "POST", "/v1/changes/"+id+"/approve", agent); code != 403 {
		t.Fatalf("an agent cannot approve: %d", code)
	}
	if code, _ := call(t, h, "POST", "/v1/changes/"+id+"/approve", `{"actor":{"kind":"human","id":"maria"}}`); code != 200 {
		t.Fatalf("person approves: %d", code)
	}
	if code, _ := call(t, h, "POST", "/v1/changes/"+id+"/apply", agent, "Idempotency-Key", "k-00000004"); code != 200 {
		t.Fatalf("agent applies an approved change: %d", code)
	}
}

func TestStalePreviewIsRefused(t *testing.T) {
	f, h := setup()
	_, pv := call(t, h, "POST", "/v1/clusters/c1/changes/preview", `{"nodes":4}`)
	f.SetSpec("c1", f.spec) // someone else changed the cluster in between
	if code, out := call(t, h, "POST", "/v1/changes/"+pv["id"].(string)+"/apply", agent, "Idempotency-Key", "k-00000005"); code != 409 {
		t.Fatalf("%d %v", code, out)
	}
}

func TestBadRequests(t *testing.T) {
	_, h := setup()
	if code, _ := call(t, h, "POST", "/v1/clusters/c1/changes/preview", `{"version":"v1.15.5"}`); code != 400 {
		t.Fatal("no-op change")
	}
	if code, _ := call(t, h, "POST", "/v1/clusters/c1/changes/preview", `{"shards":9}`); code != 400 {
		t.Fatal("unknown field")
	}
	if code, _ := call(t, h, "POST", "/v1/clusters/nope/changes/preview", `{"nodes":4}`); code != 404 {
		t.Fatal("unknown cluster")
	}
	_, pv := call(t, h, "POST", "/v1/clusters/c1/changes/preview", `{"nodes":4}`)
	if code, _ := call(t, h, "POST", "/v1/changes/"+pv["id"].(string)+"/apply", agent); code != 400 {
		t.Fatal("missing Idempotency-Key")
	}
	if code, tools := call(t, h, "GET", "/v1/agent-tools", ""); code != 200 && tools == nil {
		t.Fatal("tools")
	}
}
