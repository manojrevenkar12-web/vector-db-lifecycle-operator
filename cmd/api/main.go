// Command api serves the change API over a simulated fleet, so it can be tried without a Kubernetes cluster. Each
// cluster runs the same planner as the operator; the clock advances one simulated minute every SIM_MINUTE_MS
// milliseconds (default 200).
package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/api"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/lab"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/plan"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/sim"
)

type fleet struct {
	mu       sync.Mutex
	clusters map[string]*sim.Cluster
	gens     map[string]int
	status   map[string]model.Decision
}

func (f *fleet) IDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.clusters))
	for id := range f.clusters {
		out = append(out, id)
	}
	return out
}

func (f *fleet) Spec(id string) (model.Spec, int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clusters[id]
	if !ok {
		return model.Spec{}, 0, false
	}
	return c.Spec, f.gens[id], true
}

func (f *fleet) SetSpec(id string, s model.Spec) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clusters[id].Spec = s
	f.gens[id]++
	return f.gens[id]
}

func (f *fleet) Observe(id string) model.Observed {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clusters[id].Observe()
}

func (f *fleet) Status(id string) model.Decision {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[id]
}

func (f *fleet) step() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, c := range f.clusters {
		d := plan.Plan(c.Spec, c.Observe(), plan.Safe())
		for _, a := range d.Actions {
			c.Apply(a)
		}
		f.status[id] = d
		c.Tick()
	}
}

func main() {
	w, err := lab.LoadWorld("dev")
	if err != nil {
		log.Fatal(err)
	}
	w.Faults.CrashPerNodeDay = 0.2
	f := &fleet{clusters: map[string]*sim.Cluster{}, gens: map[string]int{}, status: map[string]model.Decision{}}
	for _, cp := range lab.Fleet(w, 11)[:8] {
		f.clusters[cp.ID] = sim.NewCluster(11, cp.ID, cp.Spec, cp.Collections, cp.Spec.Nodes*w.ShardsPerNode, w.ShardGB, w.Faults, 1_000_000)
		f.gens[cp.ID] = 1
	}
	ms, _ := strconv.Atoi(os.Getenv("SIM_MINUTE_MS"))
	if ms <= 0 {
		ms = 200
	}
	go func() {
		for range time.Tick(time.Duration(ms) * time.Millisecond) {
			f.step()
		}
	}()
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	log.Printf("change API on %s over a simulated fleet of %d clusters", addr, len(f.clusters))
	srv := &http.Server{Addr: addr, Handler: api.New(f).Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
