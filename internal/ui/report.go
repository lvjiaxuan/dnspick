package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"

	"github.com/palemoky/dnspick/internal/dnsbench"
	"github.com/palemoky/dnspick/internal/i18n"
)

// PrintResultsTable prints a formatted result table using tablewriter.
func PrintResultsTable(results []dnsbench.Result) {
	table := tablewriter.NewWriter(os.Stdout)
	table.Header(i18n.L().TableCol)

	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()

	for i, r := range results {
		rateStr := fmt.Sprintf("%.1f%% (%d/%d)", r.SuccessRate*100, r.Successes, r.Total)
		if r.SuccessRate < 1.0 {
			rateStr = red(rateStr)
		} else {
			rateStr = green(rateStr)
		}

		name := r.Name
		if r.IsSystem {
			name += i18n.L().SystemSuffix
		}
		table.Append([]string{
			fmt.Sprintf("%d", i+1),
			name,
			r.Address,
			r.AvgTime.Round(time.Microsecond).String(),
			rateStr,
			fmt.Sprintf("%.2f", r.Score),
		})
	}
	table.Render()
}

// recommendThreshold is the minimum success rate for a server to be recommended.
// maxRecommendations caps how many are surfaced (the "Top N").
const (
	recommendThreshold = 0.98
	maxRecommendations = 3
)

// Recommendation is a recommended server together with its overall rank (1-based)
// in the full, score-descending result list.
type Recommendation struct {
	Rank int
	dnsbench.Result
}

// topRecommendations selects the reliably-performing servers worth recommending,
// preserving their overall ranking, capped at maxRecommendations.
func topRecommendations(results []dnsbench.Result) []Recommendation {
	var top []Recommendation
	for i, r := range results {
		if r.SuccessRate <= recommendThreshold {
			continue
		}
		top = append(top, Recommendation{Rank: i + 1, Result: r})
		if len(top) >= maxRecommendations {
			break
		}
	}
	return top
}

// PrintRecommendations prints the top recommendations.
func PrintRecommendations(results []dnsbench.Result) {
	palette := []*color.Color{
		color.New(color.FgGreen, color.Bold),
		color.New(color.FgYellow),
		color.New(color.FgCyan),
	}

	top := topRecommendations(results)
	for i, best := range top {
		palette[i].Printf("#%d: %s (%s)\n", i+1, best.Name, best.Address)
		fmt.Printf(i18n.L().RecommendLine,
			best.Score, best.AvgTime.Round(time.Microsecond).String(), best.SuccessRate*100)
	}
	if len(top) == 0 {
		color.New(color.FgRed).Println(i18n.L().NoGoodDNS)
	}

	if msg, ok := systemDNSVerdict(results); ok {
		c := color.New(color.FgGreen, color.Bold)
		if strings.HasPrefix(msg, "⚠") {
			c = color.New(color.FgYellow, color.Bold)
		}
		fmt.Println()
		c.Println(msg)
	}
}

const (
	// switchLatencyThreshold: if the system DNS is slower than the best by less
	// than this, the gap is treated as insignificant and no switch is suggested.
	switchLatencyThreshold = 15 * time.Millisecond
	// switchSuccessMargin: if the system DNS success rate trails the best by more
	// than this, a switch is suggested even when latency is close.
	switchSuccessMargin = 0.05
)

// VerdictKind classifies the system DNS conclusion in a stable, machine-readable
// way, independent of the localized message. It is part of the --json contract.
type VerdictKind string

const (
	VerdictAllFailed  VerdictKind = "all_failed"  // system DNS failed every query
	VerdictBest       VerdictKind = "best"        // system DNS is already the top server
	VerdictGoodEnough VerdictKind = "good_enough" // not the best, but close enough to keep
	VerdictSwitch     VerdictKind = "switch"      // a clearly better server exists
)

// systemEval is the structured outcome of comparing the system DNS against the
// best server. ok (from evalSystemDNS) is false when there is no system DNS.
type systemEval struct {
	kind       VerdictKind
	sys, best  dnsbench.Result
	rank       int // 1-based rank of the system DNS
	latencyGap time.Duration
}

// evalSystemDNS locates the system DNS in the (score-descending) results and
// classifies whether it should be changed. A switch is only suggested when the
// system DNS is clearly slower (latency gap >= switchLatencyThreshold) or clearly
// less reliable, avoiding misleading the user over a few insignificant
// milliseconds. ok is false when the results contain no system DNS.
func evalSystemDNS(results []dnsbench.Result) (systemEval, bool) {
	if len(results) == 0 {
		return systemEval{}, false
	}
	sysRank := -1
	for i := range results {
		if results[i].IsSystem {
			sysRank = i
			break
		}
	}
	if sysRank < 0 {
		return systemEval{}, false
	}

	sys := results[sysRank]
	best := results[0]
	latencyGap := sys.AvgTime - best.AvgTime
	closeEnough := latencyGap < switchLatencyThreshold && best.SuccessRate-sys.SuccessRate <= switchSuccessMargin

	e := systemEval{sys: sys, best: best, rank: sysRank + 1, latencyGap: latencyGap}
	switch {
	case sys.Successes == 0:
		e.kind = VerdictAllFailed
	case sysRank == 0:
		e.kind = VerdictBest
	case closeEnough:
		e.kind = VerdictGoodEnough
	default:
		e.kind = VerdictSwitch
	}
	return e, true
}

// systemDNSVerdict produces a localized conclusion on whether the system default
// DNS should be changed. If the results contain no system DNS, ok is false.
// results must be sorted by score in descending order.
func systemDNSVerdict(results []dnsbench.Result) (msg string, ok bool) {
	e, ok := evalSystemDNS(results)
	if !ok {
		return "", false
	}

	m := i18n.L()
	switch e.kind {
	case VerdictAllFailed:
		return fmt.Sprintf(m.VerdictAllFailed, e.sys.Address, e.best.Name, e.best.Address), true
	case VerdictBest:
		return fmt.Sprintf(m.VerdictBest, e.sys.Address), true
	case VerdictGoodEnough:
		return fmt.Sprintf(m.VerdictGoodEnough, e.sys.Address, e.rank, e.latencyGap.Round(time.Microsecond)), true
	default:
		return fmt.Sprintf(m.VerdictSwitch, e.sys.Address, e.rank, e.best.Name, e.best.Address,
			e.sys.AvgTime.Round(time.Microsecond), e.best.AvgTime.Round(time.Microsecond)), true
	}
}
