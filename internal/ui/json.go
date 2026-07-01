package ui

import (
	"encoding/json"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/lvjiaxuan/dnspick/internal/dnsbench"
)

// jsonSchemaVersion is the version of the --json document structure. It is the
// stability contract for automated consumers: bump it on any backward-incompatible
// change so they can guard on it.
const jsonSchemaVersion = 3

// jsonReport is the top-level machine-readable benchmark output.
type jsonReport struct {
	Schema           int                `json:"schema"`
	Round            int                `json:"round,omitempty"`
	Timestamp        string             `json:"timestamp,omitempty"`
	QueriesPerDomain int                `json:"queries_per_domain"`
	ServersTested    int                `json:"servers_tested"`
	DomainsTested    int                `json:"domains_tested"`
	Results          []jsonResult       `json:"results"`
	Recommendation   jsonRecommendation `json:"recommendation"`
	Ports            []int              `json:"ports,omitempty"`
	PortResults      []jsonPortResult   `json:"port_results,omitempty"`
}

// jsonResult is a single server's result. Latency is expressed in milliseconds
// (rounded to microsecond precision) so consumers needn't parse Go durations.
type jsonResult struct {
	Rank         int                `json:"rank"`
	Name         string             `json:"name"`
	Address      string             `json:"address"`
	Protocol     dnsbench.Protocol  `json:"protocol"`
	IsSystem     bool               `json:"is_system"`
	AvgLatencyMs float64            `json:"avg_latency_ms"`
	SuccessRate  float64            `json:"success_rate"`
	Successes    int                `json:"successes"`
	Total        int                `json:"total"`
	Score        float64            `json:"score"`
	Resolutions  []jsonResolution   `json:"resolutions,omitempty"`
}

// jsonResolution is a single domain's resolution result for a DNS server.
type jsonResolution struct {
	Domain   string   `json:"domain"`
	IPs      []string `json:"ips"`
	Category string   `json:"category"`
}

// jsonPortResult is the TCP port connectivity test result for a single IP:port.
type jsonPortResult struct {
	IP         string  `json:"ip"`
	Port       int     `json:"port"`
	OK         bool    `json:"ok"`
	LatencyMs  float64 `json:"latency_ms,omitempty"`
}

type jsonRecommendation struct {
	Top       []jsonTop          `json:"top"`
	SystemDNS *jsonSystemVerdict `json:"system_dns,omitempty"`
}

type jsonTop struct {
	Rank     int               `json:"rank"`
	Name     string            `json:"name"`
	Address  string            `json:"address"`
	Protocol dnsbench.Protocol `json:"protocol"`
}

// jsonSystemVerdict is the conclusion about the system default DNS. Verdict is a
// stable enum (see VerdictKind); should_switch is the actionable boolean.
// is_internal_dns is true when the address is a private (RFC 1918 / RFC 4193) or
// loopback resolver, signalling that switching to an external DNS may break
// internal hostname resolution.
type jsonSystemVerdict struct {
	Name          string      `json:"name"`
	Address       string      `json:"address"`
	Rank          int         `json:"rank"`
	Verdict       VerdictKind `json:"verdict"`
	ShouldSwitch  bool        `json:"should_switch"`
	IsInternalDNS bool        `json:"is_internal_dns"`
}

// WriteJSON serializes the benchmark results as indented JSON to w. domains is the
// number of domains tested; ports is the list of TCP ports tested for connectivity;
// round and timestamp are populated only in polling mode; the rest of the metadata
// is derived from results, which must be sorted by score in descending order.
func WriteJSON(w io.Writer, results []dnsbench.Result, queriesPerDomain, domains int, ports []int, round int, timestamp string) error {
	rep := jsonReport{
		Schema:           jsonSchemaVersion,
		Round:            round,
		Timestamp:        timestamp,
		QueriesPerDomain: queriesPerDomain,
		ServersTested:    len(results),
		DomainsTested:    domains,
		Results:          make([]jsonResult, len(results)),
		Ports:            ports,
	}

	for i, r := range results {
		var resolutions []jsonResolution
		for _, res := range r.Resolutions {
			resolutions = append(resolutions, jsonResolution{
				Domain:   res.Domain,
				IPs:      res.IPs,
				Category: res.Category,
			})
		}
		rep.Results[i] = jsonResult{
			Rank:         i + 1,
			Name:         r.Name,
			Address:      r.Address,
			Protocol:     r.Protocol,
			IsSystem:     r.IsSystem,
			AvgLatencyMs: latencyMs(r.AvgTime),
			SuccessRate:  r.SuccessRate,
			Successes:    r.Successes,
			Total:        r.Total,
			Score:        r.Score,
			Resolutions:  resolutions,
		}
	}

	// Build deduplicated port_results from the shared PortResults map.
	if len(ports) > 0 && len(results) > 0 && len(results[0].PortResults) > 0 {
		seen := make(map[string]struct{})
		for _, r := range results {
			for _, res := range r.Resolutions {
				for _, ip := range res.IPs {
					for _, port := range ports {
						key := net.JoinHostPort(ip, strconv.Itoa(port))
						if _, ok := seen[key]; ok {
							continue
						}
						seen[key] = struct{}{}
						if pr, ok := r.PortResults[key]; ok {
							jr := jsonPortResult{
								IP:   ip,
								Port: port,
								OK:   pr.OK,
							}
							if pr.OK {
								jr.LatencyMs = latencyMs(pr.Duration)
							}
							rep.PortResults = append(rep.PortResults, jr)
						}
					}
				}
			}
		}
	}

	for _, best := range topRecommendations(results) {
		rep.Recommendation.Top = append(rep.Recommendation.Top, jsonTop{
			Rank:     best.Rank,
			Name:     best.Name,
			Address:  best.Address,
			Protocol: best.Protocol,
		})
	}

	if e, ok := evalSystemDNS(results); ok {
		rep.Recommendation.SystemDNS = &jsonSystemVerdict{
			Name:          e.sys.Name,
			Address:       e.sys.Address,
			Rank:          e.rank,
			Verdict:       e.kind,
			ShouldSwitch:  e.kind == VerdictSwitch || e.kind == VerdictAllFailed,
			IsInternalDNS: isInternalDNS(e.sys.Address),
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
