package ui

import (
	"encoding/json"
	"io"
	"time"

	"github.com/palemoky/dnspick/internal/dnsbench"
)

// jsonSchemaVersion is the version of the --json document structure. It is the
// stability contract for automated consumers: bump it on any backward-incompatible
// change so they can guard on it.
const jsonSchemaVersion = 1

// jsonReport is the top-level machine-readable benchmark output.
type jsonReport struct {
	Schema           int                `json:"schema"`
	QueriesPerDomain int                `json:"queries_per_domain"`
	ServersTested    int                `json:"servers_tested"`
	DomainsTested    int                `json:"domains_tested"`
	Results          []jsonResult       `json:"results"`
	Recommendation   jsonRecommendation `json:"recommendation"`
}

// jsonResult is a single server's result. Latency is expressed in milliseconds
// (rounded to microsecond precision) so consumers needn't parse Go durations.
type jsonResult struct {
	Rank         int     `json:"rank"`
	Name         string  `json:"name"`
	Address      string  `json:"address"`
	IsSystem     bool    `json:"is_system"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	SuccessRate  float64 `json:"success_rate"`
	Successes    int     `json:"successes"`
	Total        int     `json:"total"`
	Score        float64 `json:"score"`
}

type jsonRecommendation struct {
	Top       []jsonTop          `json:"top"`
	SystemDNS *jsonSystemVerdict `json:"system_dns,omitempty"`
}

type jsonTop struct {
	Rank    int    `json:"rank"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

// jsonSystemVerdict is the conclusion about the system default DNS. Verdict is a
// stable enum (see VerdictKind); should_switch is the actionable boolean.
type jsonSystemVerdict struct {
	Name         string      `json:"name"`
	Address      string      `json:"address"`
	Rank         int         `json:"rank"`
	Verdict      VerdictKind `json:"verdict"`
	ShouldSwitch bool        `json:"should_switch"`
}

// WriteJSON serializes the benchmark results as indented JSON to w. domains is the
// number of domains tested; the rest of the metadata is derived from results,
// which must be sorted by score in descending order.
func WriteJSON(w io.Writer, results []dnsbench.Result, queriesPerDomain, domains int) error {
	rep := jsonReport{
		Schema:           jsonSchemaVersion,
		QueriesPerDomain: queriesPerDomain,
		ServersTested:    len(results),
		DomainsTested:    domains,
		Results:          make([]jsonResult, len(results)),
	}

	for i, r := range results {
		rep.Results[i] = jsonResult{
			Rank:         i + 1,
			Name:         r.Name,
			Address:      r.Address,
			IsSystem:     r.IsSystem,
			AvgLatencyMs: latencyMs(r.AvgTime),
			SuccessRate:  r.SuccessRate,
			Successes:    r.Successes,
			Total:        r.Total,
			Score:        r.Score,
		}
	}

	for _, best := range topRecommendations(results) {
		rep.Recommendation.Top = append(rep.Recommendation.Top, jsonTop{
			Rank:    best.Rank,
			Name:    best.Name,
			Address: best.Address,
		})
	}

	if e, ok := evalSystemDNS(results); ok {
		rep.Recommendation.SystemDNS = &jsonSystemVerdict{
			Name:         e.sys.Name,
			Address:      e.sys.Address,
			Rank:         e.rank,
			Verdict:      e.kind,
			ShouldSwitch: e.kind == VerdictSwitch || e.kind == VerdictAllFailed,
		}
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// latencyMs converts a duration to milliseconds, rounded to microsecond precision
// to match the human-readable table.
func latencyMs(d time.Duration) float64 {
	return float64(d.Round(time.Microsecond)) / float64(time.Millisecond)
}
