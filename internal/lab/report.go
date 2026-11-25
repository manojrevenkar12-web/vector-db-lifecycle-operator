package lab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	startMark = "<!-- results:start -->"
	endMark   = "<!-- results:end -->"
)

type row struct {
	label  string
	value  func(Summary) float64
	format string
	better string // lower | higher
}

var mainRows = []row{
	{"Availability (share of shard-minutes with a serving copy)", func(s Summary) float64 { return s.Availability }, "%.4f", "higher"},
	{"Shard-minutes with no serving copy, caused by the operator's own changes", func(s Summary) float64 { return s.UnavailableByOps }, "%.0f", "lower"},
	{"Shard-minutes with no serving copy, all causes", func(s Summary) float64 { return s.UnavailableShardMinutes }, "%.0f", "lower"},
	{"Shards losing every copy on clusters with 2+ replicas", func(s Summary) float64 { return s.LossEventsReplicated }, "%.2f", "lower"},
	{"Shards losing every copy, all clusters", func(s Summary) float64 { return s.LossEvents }, "%.2f", "lower"},
	{"Shards restored from a backup", func(s Summary) float64 { return s.ShardsRestored }, "%.2f", "higher"},
	{"Shards still without their data at the end", func(s Summary) float64 { return s.ShardsLostAtEnd }, "%.2f", "lower"},
	{"Under-replicated shard-hours", func(s Summary) float64 { return s.UnderReplicatedMinutes / 60 }, "%.0f", "lower"},
	{"Requested changes completed (share)", func(s Summary) float64 { return s.ChangesCompletedShare }, "%.3f", "higher"},
	{"Mean minutes per change", func(s Summary) float64 { return s.MeanChangeMinutes }, "%.1f", "lower"},
	{"DR clusters restored after losing their region (share)", func(s Summary) float64 { return s.DRRecoveredShare }, "%.2f", "higher"},
	{"Worst data loss on a region restore (minutes)", func(s Summary) float64 { return s.DRWorstRPOMinutes }, "%.0f", "lower"},
	{"Mean time to restore a region (hours)", func(s Summary) float64 { return s.DRMeanRTOHours }, "%.2f", "lower"},
	{"Temporary copies during upgrades (GB-hours)", func(s Summary) float64 { return s.SurgeGBHours }, "%.0f", "lower"},
}

var ablationCols = []row{
	{"Lost at end", func(s Summary) float64 { return s.ShardsLostAtEnd }, "%.2f", "lower"},
	{"Loss events", func(s Summary) float64 { return s.LossEvents }, "%.2f", "lower"},
	{"Unavailable by ops (shard-min)", func(s Summary) float64 { return s.UnavailableByOps }, "%.0f", "lower"},
	{"Under-replicated (shard-h)", func(s Summary) float64 { return s.UnderReplicatedMinutes / 60 }, "%.0f", "lower"},
	{"DR restored", func(s Summary) float64 { return s.DRRecoveredShare }, "%.2f", "higher"},
	{"Worst RPO (min)", func(s Summary) float64 { return s.DRWorstRPOMinutes }, "%.0f", "lower"},
	{"Mean RTO (h)", func(s Summary) float64 { return s.DRMeanRTOHours }, "%.2f", "lower"},
}

func cell(r row, s Summary) string {
	if strings.Contains(r.label, "region") || strings.Contains(r.label, "RPO") || strings.Contains(r.label, "RTO") {
		if s.DRRecovered == 0 && r.better == "lower" {
			return "–"
		}
	}
	return fmt.Sprintf(r.format, r.value(s))
}

func mainTable(r SplitResult) string {
	var b strings.Builder
	a, o := r.Designs[r.Main[0]], r.Designs[r.Main[1]]
	fmt.Fprintf(&b, "| Measure (mean of %d fleet runs) | %s | %s |\n|---|---:|---:|\n", len(r.Seeds), r.Names[r.Main[0]], r.Names[r.Main[1]])
	for _, x := range mainRows {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", x.label, cell(x, a), cell(x, o))
	}
	return b.String()
}

func ablationTable(r SplitResult) string {
	var b strings.Builder
	b.WriteString("| Design |")
	for _, c := range ablationCols {
		b.WriteString(" " + c.label + " |")
	}
	b.WriteString("\n|---|" + strings.Repeat("---:|", len(ablationCols)) + "\n")
	op := r.Designs[r.Main[1]]
	for _, n := range append([]string{r.Main[1]}, r.Ablations...) {
		s := r.Designs[n]
		fmt.Fprintf(&b, "| %s |", r.Names[n])
		for _, c := range ablationCols {
			v := cell(c, s)
			worse := (c.better == "lower" && c.value(s) > c.value(op)*1.0001+1e-9) || (c.better == "higher" && c.value(s) < c.value(op)*0.9999-1e-9)
			if n != r.Main[1] && worse && v != "–" {
				v = "**" + v + "**"
			}
			b.WriteString(" " + v + " |")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func gateTable(r SplitResult) string {
	var b strings.Builder
	b.WriteString("| Gate | Value | Limit | Result |\n|---|---:|---:|---|\n")
	for _, g := range r.Gates.Operator {
		st := "pass"
		if !g.Pass {
			st = "**fail**"
		}
		op := "≤"
		if g.Kind == "min" {
			op = "≥"
		}
		fmt.Fprintf(&b, "| %s | %.4g | %s %g | %s |\n", g.Gate, g.Value, op, g.Limit, st)
	}
	return b.String()
}

func passed(r SplitResult) (int, int) {
	k := 0
	for _, g := range r.Gates.Operator {
		if g.Pass {
			k++
		}
	}
	return k, len(r.Gates.Operator)
}

func fleetLine(r SplitResult) string {
	return fmt.Sprintf("%d clusters, %d nodes, about %d shards, %.0f simulated days per run; %d upgrades, %d scale-ups, %d scale-downs requested; %d clusters with a DR region; seeds %v.",
		r.Fleet.Clusters, r.Fleet.Nodes, r.Fleet.Shards, r.Fleet.Days, r.Fleet.Changes["upgrade"], r.Fleet.Changes["scaleUp"], r.Fleet.Changes["scaleDown"], r.Fleet.DR, r.Seeds)
}

func dashboard(dev SplitResult, ho *SplitResult) string {
	var b strings.Builder
	k, n := passed(dev)
	fmt.Fprintf(&b, "_Generated by `go run ./cmd/lab dev` from `reports/*.json`; do not edit by hand._\n\n")
	fmt.Fprintf(&b, "**Dev fleet:** %s Gates: %d of %d pass.\n\n", fleetLine(dev), k, n)
	for _, n := range dev.Main {
		fmt.Fprintf(&b, "- **%s**: %s.\n", dev.Names[n], dev.Labels[n])
	}
	b.WriteString("\n")
	b.WriteString(mainTable(dev))
	b.WriteString("\n**Each safeguard switched off on its own** (bold = worse than the full operator):\n\n")
	b.WriteString(ablationTable(dev))
	if ho != nil {
		k, n := passed(*ho)
		fmt.Fprintf(&b, "\n**Held-out fleet, run once behind `reports/heldout.lock`** (harsher faults): %s Gates: %d of %d pass.\n\n", fleetLine(*ho), k, n)
		b.WriteString(mainTable(*ho))
		b.WriteString("\n")
		b.WriteString(gateTable(*ho))
	} else {
		b.WriteString("\n_The held-out fleet has not been run yet._\n")
	}
	return b.String()
}

// WriteReports renders REPORT.md, the README results block and the console's data from reports/*.json.
func WriteReports() error {
	root := Root()
	dev, ok, err := Load("dev")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("reports/dev.json is missing: run `go run ./cmd/lab dev`")
	}
	var ho *SplitResult
	if h, ok, err := Load("heldout"); err != nil {
		return err
	} else if ok {
		ho = &h
	}
	var rep strings.Builder
	rep.WriteString("# Results\n\n")
	rep.WriteString(dashboard(dev, ho))
	rep.WriteString("\n## Dev gates\n\n")
	rep.WriteString(gateTable(dev))
	rep.WriteString("\n## Per seed (dev)\n\n| Seed | Design | Availability | Unavailable by ops | Lost at end | Changes done | DR restored |\n|---|---|---:|---:|---:|---:|---:|\n")
	for _, ps := range dev.PerSeed {
		for _, n := range dev.Main {
			m, _ := ps[n].(map[string]any)
			f := func(k string) float64 { v, _ := m[k].(float64); return v }
			fmt.Fprintf(&rep, "| %v | %s | %.4f | %.0f | %.0f | %.3f | %.2f |\n", ps["seed"], dev.Names[n], f("availability"), f("unavailableByOps"), f("shardsLostAtEnd"), f("changesCompletedShare"), f("drRecoveredShare"))
		}
	}
	if err := os.WriteFile(filepath.Join(root, "REPORT.md"), []byte(rep.String()), 0o644); err != nil {
		return err
	}
	readme := filepath.Join(root, "README.md")
	if b, err := os.ReadFile(readme); err == nil {
		i, j := bytes.Index(b, []byte(startMark)), bytes.Index(b, []byte(endMark))
		if i >= 0 && j > i {
			out := append([]byte{}, b[:i+len(startMark)]...)
			out = append(out, []byte("\n"+dashboard(dev, ho))...)
			out = append(out, b[j:]...)
			if err := os.WriteFile(readme, out, 0o644); err != nil {
				return err
			}
		}
	}
	web := filepath.Join(root, "web", "public", "data")
	if err := os.MkdirAll(web, 0o755); err != nil {
		return err
	}
	payload := map[string]any{"dev": roundAll(dev)}
	if ho != nil {
		payload["heldout"] = roundAll(*ho)
	}
	j, _ := json.MarshalIndent(payload, "", " ")
	return os.WriteFile(filepath.Join(web, "report.json"), append(j, '\n'), 0o644)
}
