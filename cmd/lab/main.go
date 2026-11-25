// Command lab runs the study and writes the reports: lab dev | heldout | report | gate [split] | probe
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/lab"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: lab dev | heldout | report | gate [dev|heldout] | probe")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "dev":
		r, err := lab.RunSplit("dev")
		must(err)
		must(lab.Write(r))
		must(lab.WriteReports())
	case "heldout":
		_, err := lab.RunHeldoutOnce()
		if errors.Is(err, lab.ErrLocked) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		must(err)
		must(lab.WriteReports())
	case "report":
		must(lab.WriteReports())
	case "gate":
		split := "dev"
		if len(os.Args) > 2 {
			split = os.Args[2]
		}
		r, ok, err := lab.Load(split)
		must(err)
		if !ok {
			fmt.Fprintf(os.Stderr, "reports/%s.json is missing\n", split)
			os.Exit(1)
		}
		for _, g := range r.Gates.Operator {
			st := "PASS"
			if !g.Pass {
				st = "FAIL"
			}
			fmt.Printf("%s %s: %.4g (%s %g)\n", st, g.Gate, g.Value, g.Kind, g.Limit)
		}
		if !r.Gates.Pass {
			os.Exit(1)
		}
	case "probe":
		r, err := lab.RunSplit("dev")
		must(err)
		b, _ := json.MarshalIndent(r.Designs, "", " ")
		fmt.Println(string(b))
		for _, g := range r.Gates.Operator {
			fmt.Println(g.Pass, g.Gate, g.Value, g.Limit)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown command", os.Args[1])
		os.Exit(2)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
