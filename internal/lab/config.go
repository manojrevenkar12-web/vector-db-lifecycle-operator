// Package lab runs every design over the same simulated fleet, scores it against the truth, checks the gates and
// writes the reports.
package lab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/plan"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/sim"
)

// Change is a customer request at a minute.
type Change struct {
	At    int     `json:"at"`
	Kind  string  `json:"kind"` // upgrade | scaleUp | scaleDown
	Share float64 `json:"share"`
}

// World is one variant of config/world.json.
type World struct {
	Clusters           int                `json:"clusters"`
	Minutes            int                `json:"minutes"`
	Nodes              map[string]float64 `json:"nodes"`
	ReplicationFactor  map[string]float64 `json:"replicationFactor"`
	Collections        [2]int             `json:"collections"`
	ShardsPerNode      int                `json:"shardsPerNode"`
	ShardGB            [2]float64         `json:"shardGB"`
	DRShare            float64            `json:"drShare"`
	BackupEveryMinutes int                `json:"backupEveryMinutes"`
	BackupRetain       int                `json:"backupRetain"`
	Changes            []Change           `json:"changes"`
	Versions           []string           `json:"versions"`
	Faults             sim.Faults         `json:"faults"`
}

// Design is one entry of config/designs.json.
type Design struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Base  string `json:"base"`
	Off   string `json:"off"`
}

// Designs is config/designs.json.
type Designs struct {
	Main      []string          `json:"main"`
	Ablations []string          `json:"ablations"`
	Designs   map[string]Design `json:"designs"`
}

// Gates is config/gates.json.
type Gates struct {
	Operator struct {
		ShardsLostAtEndMax        float64 `json:"shardsLostAtEndMax"`
		ReplicatedLossVsAsIsMax   float64 `json:"replicatedLossVsAsIsMax"`
		UnavailableByOpsVsAsIsMax float64 `json:"unavailableByOpsVsAsIsMax"`
		ChangesCompletedMin       float64 `json:"changesCompletedMin"`
		DRRecoveredMin            float64 `json:"drRecoveredMin"`
		DRWorstRpoMinutesMax      float64 `json:"drWorstRpoMinutesMax"`
	} `json:"operator"`
	EachSafeguardMatters bool `json:"eachSafeguardMatters"`
}

// Seeds is config/seeds.json.
type Seeds struct {
	Dev     []int64 `json:"dev"`
	Heldout []int64 `json:"heldout"`
}

// Root is the repository root: OPERATOR_ROOT, or the nearest parent with a config folder.
func Root() string {
	if r := os.Getenv("OPERATOR_ROOT"); r != "" {
		return r
	}
	d, _ := os.Getwd()
	for {
		if st, err := os.Stat(filepath.Join(d, "config", "designs.json")); err == nil && !st.IsDir() {
			return d
		}
		p := filepath.Dir(d)
		if p == d {
			return "."
		}
		d = p
	}
}

func load(name string, v any) error {
	b, err := os.ReadFile(filepath.Join(Root(), "config", name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// LoadWorld reads one variant of the world.
func LoadWorld(variant string) (World, error) {
	var all map[string]json.RawMessage
	if err := load("world.json", &all); err != nil {
		return World{}, err
	}
	var w World
	raw, ok := all[variant]
	if !ok {
		return w, fmt.Errorf("world.json has no %q", variant)
	}
	return w, json.Unmarshal(raw, &w)
}

// LoadDesigns, LoadGates and LoadSeeds read the rest of config/.
func LoadDesigns() (Designs, error) { var d Designs; return d, load("designs.json", &d) }
func LoadGates() (Gates, error)     { var g Gates; return g, load("gates.json", &g) }
func LoadSeeds() (Seeds, error)     { var s Seeds; return s, load("seeds.json", &s) }

// Options turns a design into planner options.
func (d Design) Options() (plan.Options, error) {
	o := plan.Safe()
	if d.Base == "as_is" {
		o = plan.AsIs()
	}
	switch d.Off {
	case "":
	case "waitForActive":
		o.WaitForActive = false
	case "drain":
		o.Drain = false
	case "repair":
		o.Repair = false
	case "surge":
		o.Surge = false
	case "verifyBackups":
		o.VerifyBackups = false
	case "shipBackups":
		o.ShipBackups = false
	default:
		return o, fmt.Errorf("unknown safeguard %q", d.Off)
	}
	return o, nil
}

func pick(weights map[string]float64, u float64) int {
	keys := make([]string, 0, len(weights))
	total := 0.0
	for k, w := range weights {
		keys = append(keys, k)
		total += w
	}
	sort.Strings(keys)
	acc := 0.0
	for _, k := range keys {
		acc += weights[k] / total
		if u < acc {
			n, _ := strconv.Atoi(k)
			return n
		}
	}
	n, _ := strconv.Atoi(keys[len(keys)-1])
	return n
}
