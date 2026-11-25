package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
)

// SplitResult is everything one split produces; reports/<split>.json holds it.
type SplitResult struct {
	Split     string             `json:"split"`
	Seeds     []int64            `json:"seeds"`
	Main      []string           `json:"main"`
	Ablations []string           `json:"ablations"`
	Names     map[string]string  `json:"names"`
	Labels    map[string]string  `json:"labels"`
	Designs   map[string]Summary `json:"designs"`
	PerSeed   []map[string]any   `json:"perSeed"`
	Fleet     FleetInfo          `json:"fleet"`
	Gates     GateResult         `json:"gates"`
}

// FleetInfo describes the fleet for the reports.
type FleetInfo struct {
	Clusters int            `json:"clusters"`
	Days     float64        `json:"days"`
	Nodes    int            `json:"nodes"`
	Shards   int            `json:"shards"`
	Changes  map[string]int `json:"changes"`
	DR       int            `json:"dr"`
}

// RunSplit runs every design on every seed of a split.
func RunSplit(split string) (SplitResult, error) {
	variant := "dev"
	if split == "heldout" {
		variant = "heldout"
	}
	w, err := LoadWorld(variant)
	if err != nil {
		return SplitResult{}, err
	}
	ds, err := LoadDesigns()
	if err != nil {
		return SplitResult{}, err
	}
	sd, err := LoadSeeds()
	if err != nil {
		return SplitResult{}, err
	}
	seeds := sd.Dev
	if split == "heldout" {
		seeds = sd.Heldout
	}
	names := append(append([]string{}, ds.Main...), ds.Ablations...)
	type job struct {
		seed int64
		name string
	}
	out := map[job]Summary{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 2)
	var firstErr error
	for _, s := range seeds {
		fleet := Fleet(w, s)
		for _, n := range names {
			d, ok := ds.Designs[n]
			if !ok {
				return SplitResult{}, fmt.Errorf("design %q missing", n)
			}
			opt, err := d.Options()
			if err != nil {
				return SplitResult{}, err
			}
			wg.Add(1)
			go func(s int64, n string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				rs := make([]Result, len(fleet))
				for i, cp := range fleet {
					rs[i] = RunCluster(w, s, cp, opt)
				}
				mu.Lock()
				out[job{s, n}] = Summarise(rs)
				mu.Unlock()
			}(s, n)
		}
	}
	wg.Wait()
	if firstErr != nil {
		return SplitResult{}, firstErr
	}
	res := SplitResult{Split: split, Seeds: seeds, Main: ds.Main, Ablations: ds.Ablations, Names: map[string]string{}, Labels: map[string]string{}, Designs: map[string]Summary{}}
	for _, n := range names {
		res.Names[n] = ds.Designs[n].Name
		res.Labels[n] = ds.Designs[n].Label
		var per []Summary
		for _, s := range seeds {
			per = append(per, out[job{s, n}])
		}
		res.Designs[n] = meanSummary(per)
	}
	for _, s := range seeds {
		row := map[string]any{"seed": s}
		for _, n := range ds.Main {
			x := out[job{s, n}]
			row[n] = map[string]float64{"availability": x.Availability, "unavailableByOps": x.UnavailableByOps, "shardsLostAtEnd": x.ShardsLostAtEnd, "changesCompletedShare": x.ChangesCompletedShare, "drRecoveredShare": x.DRRecoveredShare}
		}
		res.PerSeed = append(res.PerSeed, row)
	}
	fl := Fleet(w, seeds[0])
	res.Fleet = FleetInfo{Clusters: len(fl), Days: float64(w.Minutes) / 1440, Changes: map[string]int{}}
	for _, cp := range fl {
		res.Fleet.Nodes += cp.Spec.Nodes
		res.Fleet.Shards += cp.Collections * cp.Spec.Nodes * w.ShardsPerNode
		if cp.Spec.DR != nil {
			res.Fleet.DR++
		}
		for _, ch := range cp.Changes {
			res.Fleet.Changes[ch.Kind]++
		}
	}
	g, err := LoadGates()
	if err != nil {
		return res, err
	}
	res.Gates = CheckGates(res, g)
	return res, nil
}

// meanSummary averages every numeric field over seeds.
func meanSummary(xs []Summary) Summary {
	var m Summary
	mv := reflect.ValueOf(&m).Elem()
	for _, x := range xs {
		xv := reflect.ValueOf(x)
		for i := 0; i < mv.NumField(); i++ {
			switch mv.Field(i).Kind() {
			case reflect.Float64:
				mv.Field(i).SetFloat(mv.Field(i).Float() + xv.Field(i).Float()/float64(len(xs)))
			case reflect.Int:
				mv.Field(i).SetInt(xv.Field(i).Int())
			}
		}
	}
	// the worst RPO is the worst of the runs, not the mean
	for _, x := range xs {
		if x.DRWorstRPOMinutes > m.DRWorstRPOMinutes {
			m.DRWorstRPOMinutes = x.DRWorstRPOMinutes
		}
	}
	return m
}

// Gate is one release check.
type Gate struct {
	Gate  string  `json:"gate"`
	Value float64 `json:"value"`
	Limit float64 `json:"limit"`
	Kind  string  `json:"kind"` // min | max
	Pass  bool    `json:"pass"`
}

// GateResult is every gate for one split.
type GateResult struct {
	Operator []Gate `json:"operator"`
	Pass     bool   `json:"pass"`
}

// CheckGates compares the operator with the as-is and checks that each safeguard matters.
func CheckGates(r SplitResult, g Gates) GateResult {
	o, a := r.Designs["operator"], r.Designs["as_is"]
	add := func(out []Gate, name string, v, lim float64, kind string) []Gate {
		pass := v <= lim
		if kind == "min" {
			pass = v >= lim
		}
		return append(out, Gate{name, v, lim, kind, pass})
	}
	var gs []Gate
	gs = add(gs, "Shards still without their data at the end (mean per fleet run)", o.ShardsLostAtEnd, g.Operator.ShardsLostAtEndMax, "max")
	gs = add(gs, "Shards losing every copy on clusters with 2 or more replicas, ratio to the as-is", ratio(o.LossEventsReplicated, math1(a.LossEventsReplicated)), g.Operator.ReplicatedLossVsAsIsMax, "max")
	gs = add(gs, "Shard-minutes with no serving copy caused by the operator's own changes, ratio to the as-is", ratio(o.UnavailableByOps, math1(a.UnavailableByOps)), g.Operator.UnavailableByOpsVsAsIsMax, "max")
	gs = add(gs, "Requested changes completed", o.ChangesCompletedShare, g.Operator.ChangesCompletedMin, "min")
	gs = add(gs, "Clusters with DR restored after losing their region", o.DRRecoveredShare, g.Operator.DRRecoveredMin, "min")
	gs = add(gs, "Worst data loss on a region restore, minutes", o.DRWorstRPOMinutes, g.Operator.DRWorstRpoMinutesMax, "max")
	if g.EachSafeguardMatters {
		worse := 0.0
		total := 0.0
		for _, n := range r.Ablations {
			total++
			x := r.Designs[n]
			if x.ShardsLostAtEnd > o.ShardsLostAtEnd || x.LossEvents > o.LossEvents || x.UnavailableByOps > o.UnavailableByOps*1.05 || x.DRRecoveredShare < o.DRRecoveredShare || x.DRWorstRPOMinutes > o.DRWorstRPOMinutes || x.UnderReplicatedMinutes > o.UnderReplicatedMinutes*1.05 || x.RestoreLossHours > o.RestoreLossHours*1.05 || x.DRMeanRTOHours > o.DRMeanRTOHours*1.05 {
				worse++
			}
		}
		gs = add(gs, "Ablations worse than the operator on at least one measure (share)", ratio(worse, total), 1, "min")
	}
	pass := true
	for _, x := range gs {
		pass = pass && x.Pass
	}
	return GateResult{gs, pass}
}

func math1(x float64) float64 {
	if x < 1 {
		return 1
	}
	return x
}

// ErrLocked means the held-out fleet has already been run.
var ErrLocked = errors.New("reports/heldout.lock exists: the held-out fleet has been run; add new seeds under a new name and an ADR")

// ConfigHash fingerprints config/ so the lock records what the held-out run used.
func ConfigHash() string {
	h := sha256.New()
	entries, _ := filepath.Glob(filepath.Join(Root(), "config", "*.json"))
	sort.Strings(entries)
	for _, p := range entries {
		b, _ := os.ReadFile(p)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Write stores a split's result.
func Write(r SplitResult) error {
	dir := filepath.Join(Root(), "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(roundAll(r), "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, r.Split+".json"), append(b, '\n'), 0o644)
}

// RunHeldoutOnce writes the lock first, then runs the held-out fleet.
func RunHeldoutOnce() (SplitResult, error) {
	lock := filepath.Join(Root(), "reports", "heldout.lock")
	if _, err := os.Stat(lock); err == nil {
		return SplitResult{}, ErrLocked
	}
	sd, err := LoadSeeds()
	if err != nil {
		return SplitResult{}, err
	}
	_ = os.MkdirAll(filepath.Dir(lock), 0o755)
	b, _ := json.MarshalIndent(map[string]any{"seeds": sd.Heldout, "configSha256": ConfigHash()}, "", " ")
	if err := os.WriteFile(lock, append(b, '\n'), 0o644); err != nil {
		return SplitResult{}, err
	}
	r, err := RunSplit("heldout")
	if err != nil {
		return r, err
	}
	return r, Write(r)
}

// Load reads a split's stored result.
func Load(split string) (SplitResult, bool, error) {
	b, err := os.ReadFile(filepath.Join(Root(), "reports", split+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return SplitResult{}, false, nil
	}
	if err != nil {
		return SplitResult{}, false, err
	}
	var r SplitResult
	return r, true, json.Unmarshal(b, &r)
}
