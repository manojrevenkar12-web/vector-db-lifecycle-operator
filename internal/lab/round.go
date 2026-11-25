package lab

import (
	"encoding/json"
	"math"
)

// roundAll rounds every float to 5 decimals so the stored reports are stable across machines.
func roundAll(v any) any {
	b, _ := json.Marshal(v)
	var x any
	_ = json.Unmarshal(b, &x)
	return roundAny(x)
}

func roundAny(x any) any {
	switch t := x.(type) {
	case float64:
		return math.Round(t*1e5) / 1e5
	case map[string]any:
		for k, v := range t {
			t[k] = roundAny(v)
		}
		return t
	case []any:
		for i, v := range t {
			t[i] = roundAny(v)
		}
		return t
	}
	return x
}
