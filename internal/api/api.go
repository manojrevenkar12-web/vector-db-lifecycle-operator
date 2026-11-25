// Package api is the change API for people and AI agents. A change is previewed first: the preview says what will
// restart, what will be copied, what could go wrong and whether a person has to approve it. Applying needs the
// preview's id, fails if the cluster changed since the preview, and is idempotent per Idempotency-Key. Agents can
// apply only changes that do not need a person, or that a person approved.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
)

// Backend is what the API needs from a cluster: its spec, a way to change it, and what can be observed.
type Backend interface {
	IDs() []string
	Spec(id string) (model.Spec, int, bool) // spec, generation, found
	SetSpec(id string, s model.Spec) int    // returns the new generation
	Observe(id string) model.Observed
	Status(id string) model.Decision
}

// Patch is a requested change; nil fields stay as they are.
type Patch struct {
	Version           *string `json:"version,omitempty"`
	Nodes             *int    `json:"nodes,omitempty"`
	ReplicationFactor *int    `json:"replicationFactor,omitempty"`
	DR                *bool   `json:"dr,omitempty"`
	DRRegion          string  `json:"drRegion,omitempty"`
}

// Effects is what the preview expects the change to do.
type Effects struct {
	Restarts          int      `json:"restarts"`
	NodesAdded        int      `json:"nodesAdded"`
	NodesRemoved      int      `json:"nodesRemoved"`
	ReplicasToMove    int      `json:"replicasToMove"`
	TemporaryCopies   int      `json:"temporaryCopies"`
	Risks             []string `json:"risks"`
	NeedsPerson       bool     `json:"needsPerson"`
	NeedsPersonReason []string `json:"needsPersonReason"`
}

// Preview is a change that has been checked but not applied.
type Preview struct {
	ID         string     `json:"id"`
	Cluster    string     `json:"cluster"`
	Generation int        `json:"generation"`
	From       model.Spec `json:"from"`
	To         model.Spec `json:"to"`
	Effects    Effects    `json:"effects"`
	ApprovedBy string     `json:"approvedBy,omitempty"`
	Applied    bool       `json:"applied"`
}

// Actor is who asks: a person or an agent.
type Actor struct {
	Kind string `json:"kind"` // human | agent
	ID   string `json:"id"`
}

// Server is the HTTP API.
type Server struct {
	mu       sync.Mutex
	b        Backend
	previews map[string]*Preview
	done     map[string][]byte // Idempotency-Key -> response body
	newID    func() string
}

// New returns a server over a backend.
func New(b Backend) *Server {
	return &Server{b: b, previews: map[string]*Preview{}, done: map[string][]byte{}, newID: func() string {
		buf := make([]byte, 6)
		_, _ = rand.Read(buf)
		return "chg-" + hex.EncodeToString(buf)
	}}
}

func minorOf(v string) (int, int, bool) {
	p := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(p) < 2 {
		return 0, 0, false
	}
	a, e1 := strconv.Atoi(p[0])
	b, e2 := strconv.Atoi(p[1])
	return a, b, e1 == nil && e2 == nil
}

// Assess works out the effects of going from one spec to another on what is observed now.
func Assess(from, to model.Spec, obs model.Observed) Effects {
	e := Effects{Risks: []string{}, NeedsPersonReason: []string{}}
	need := func(r string) { e.NeedsPerson = true; e.NeedsPersonReason = append(e.NeedsPersonReason, r) }
	if to.Version != from.Version {
		fa, fb, ok1 := minorOf(from.Version)
		ta, tb, ok2 := minorOf(to.Version)
		switch {
		case !ok1 || !ok2:
			need("version is not of the form vMAJOR.MINOR.PATCH")
		case ta < fa || (ta == fa && tb < fb):
			need("downgrade: the storage format may not go back")
		case ta != fa || tb-fb > 1:
			need("skips a minor version: upgrade one minor version at a time")
		}
		e.Restarts = len(obs.Nodes)
		for _, s := range obs.Shards {
			if len(s.Replicas) == 1 {
				e.TemporaryCopies++
			}
		}
		if e.TemporaryCopies > 0 {
			e.Risks = append(e.Risks, fmt.Sprintf("%d shard(s) have a single copy: each gets a temporary copy before its node restarts", e.TemporaryCopies))
		}
		if len(obs.Nodes) == 1 {
			e.Risks = append(e.Risks, "single-node cluster: the restart takes it offline for a few minutes")
		}
	}
	if to.Nodes > from.Nodes {
		e.NodesAdded = to.Nodes - from.Nodes
	}
	if to.Nodes < from.Nodes {
		e.NodesRemoved = from.Nodes - to.Nodes
		for _, n := range obs.Nodes {
			if n.Ordinal >= to.Nodes {
				for _, s := range obs.Shards {
					for _, r := range s.Replicas {
						if r.Peer == n.Peer {
							e.ReplicasToMove++
						}
					}
				}
			}
		}
		if to.Nodes < to.ReplicationFactor {
			need(fmt.Sprintf("%d node(s) cannot hold %d copies of each shard", to.Nodes, to.ReplicationFactor))
		}
	}
	if to.Nodes > from.Nodes+2 || from.Nodes > to.Nodes+2 {
		need("changes the node count by more than 2 at once")
	}
	if to.ReplicationFactor < from.ReplicationFactor {
		e.Risks = append(e.Risks, "fewer copies: a node failure can make shards unavailable")
		if to.ReplicationFactor < 2 {
			need("replication factor below 2: one failure can take shards offline")
		}
	}
	if from.DR != nil && to.DR == nil {
		need("removes disaster recovery: backups stop leaving the region")
	}
	if to.Nodes < 1 || to.ReplicationFactor < 1 {
		need("nodes and replication factor must be at least 1")
	}
	return e
}

func apply(s model.Spec, p Patch) model.Spec {
	if p.Version != nil {
		s.Version = *p.Version
	}
	if p.Nodes != nil {
		s.Nodes = *p.Nodes
	}
	if p.ReplicationFactor != nil {
		s.ReplicationFactor = *p.ReplicationFactor
	}
	if p.DR != nil {
		if *p.DR {
			r := p.DRRegion
			if r == "" {
				r = "eu-west"
			}
			s.DR = &model.DRSpec{Region: r}
		} else {
			s.DR = nil
		}
	}
	return s
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// Handler routes the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /v1/clusters", s.list)
	mux.HandleFunc("GET /v1/clusters/{id}", s.get)
	mux.HandleFunc("POST /v1/clusters/{id}/changes/preview", s.preview)
	mux.HandleFunc("POST /v1/changes/{pid}/approve", s.approve)
	mux.HandleFunc("POST /v1/changes/{pid}/apply", s.apply)
	mux.HandleFunc("GET /v1/agent-tools", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, Tools()) })
	return mux
}

func (s *Server) list(w http.ResponseWriter, _ *http.Request) {
	ids := s.b.IDs()
	sort.Strings(ids)
	out := []map[string]any{}
	for _, id := range ids {
		sp, gen, _ := s.b.Spec(id)
		st := s.b.Status(id)
		out = append(out, map[string]any{"id": id, "spec": sp, "generation": gen, "phase": st.Phase, "message": st.Message})
	}
	writeJSON(w, 200, out)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sp, gen, ok := s.b.Spec(id)
	if !ok {
		fail(w, 404, "no such cluster")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "spec": sp, "generation": gen, "status": s.b.Status(id), "observed": s.b.Observe(id)})
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	from, gen, ok := s.b.Spec(id)
	if !ok {
		fail(w, 404, "no such cluster")
		return
	}
	var p Patch
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		fail(w, 400, "body must be a change: version, nodes, replicationFactor, dr, drRegion")
		return
	}
	to := apply(from, p)
	if to.Version == from.Version && to.Nodes == from.Nodes && to.ReplicationFactor == from.ReplicationFactor && (to.DR == nil) == (from.DR == nil) {
		fail(w, 400, "the change does not change anything")
		return
	}
	pv := &Preview{ID: s.newID(), Cluster: id, Generation: gen, From: from, To: to, Effects: Assess(from, to, s.b.Observe(id))}
	s.mu.Lock()
	s.previews[pv.ID] = pv
	s.mu.Unlock()
	writeJSON(w, 201, pv)
}

func decodeActor(w http.ResponseWriter, r *http.Request) (Actor, bool) {
	var a struct {
		Actor Actor `json:"actor"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&a); err != nil || (a.Actor.Kind != "human" && a.Actor.Kind != "agent") || a.Actor.ID == "" {
		fail(w, 400, `body must name the actor: {"actor": {"kind": "human" | "agent", "id": "..."}}`)
		return Actor{}, false
	}
	return a.Actor, true
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	a, ok := decodeActor(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pv, found := s.previews[r.PathValue("pid")]
	if !found {
		fail(w, 404, "no such preview")
		return
	}
	if a.Kind != "human" {
		fail(w, 403, "only a person can approve a change")
		return
	}
	pv.ApprovedBy = a.ID
	writeJSON(w, 200, pv)
}

// ErrStale means the cluster changed after the preview was made.
var ErrStale = errors.New("the cluster changed since this preview; preview again")

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		fail(w, 400, "Idempotency-Key header (8 to 128 characters) is required")
		return
	}
	a, ok := decodeActor(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if body, seen := s.done[key]; seen {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(body)
		return
	}
	pv, found := s.previews[r.PathValue("pid")]
	if !found {
		fail(w, 404, "no such preview")
		return
	}
	if pv.Applied {
		fail(w, 409, "this preview has already been applied")
		return
	}
	if _, gen, _ := s.b.Spec(pv.Cluster); gen != pv.Generation {
		fail(w, 409, ErrStale.Error())
		return
	}
	if a.Kind == "agent" && pv.Effects.NeedsPerson && pv.ApprovedBy == "" {
		fail(w, 403, "this change needs a person: "+strings.Join(pv.Effects.NeedsPersonReason, "; "))
		return
	}
	gen := s.b.SetSpec(pv.Cluster, pv.To)
	pv.Applied = true
	body, _ := json.Marshal(map[string]any{"cluster": pv.Cluster, "generation": gen, "spec": pv.To, "appliedBy": a, "preview": pv.ID})
	s.done[key] = body
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

// Tools describes the API to an AI agent, in the shape MCP tool lists use.
func Tools() []map[string]any {
	obj := func(props map[string]any, req ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
	}
	str := map[string]any{"type": "string"}
	integer := map[string]any{"type": "integer", "minimum": 1}
	return []map[string]any{
		{"name": "list_clusters", "description": "List clusters with their spec and current phase.", "inputSchema": obj(map[string]any{})},
		{"name": "get_cluster", "description": "Spec, status and observed nodes, shards and backups of one cluster.", "inputSchema": obj(map[string]any{"id": str}, "id")},
		{"name": "preview_change", "description": "Check a change before applying it: restarts, copies, risks, and whether a person must approve. Changes nothing.", "inputSchema": obj(map[string]any{"id": str, "version": str, "nodes": integer, "replicationFactor": integer, "dr": map[string]any{"type": "boolean"}}, "id")},
		{"name": "apply_change", "description": "Apply a preview by id. Fails if the cluster changed since the preview or if the change needs a person and none approved it.", "inputSchema": obj(map[string]any{"previewId": str, "idempotencyKey": str}, "previewId", "idempotencyKey")},
	}
}
