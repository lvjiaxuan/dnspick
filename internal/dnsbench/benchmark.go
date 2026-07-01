package dnsbench

import (
	"bytes"
	"cmp"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options controls a single benchmark run.
type Options struct {
	Servers     []Server      // servers to test; uses DefaultServers when empty
	Domains     []Domain      // test domains
	Queries     int           // number of queries per domain
	Timeout     time.Duration // timeout per query
	Concurrency int           // maximum number of servers tested concurrently
	Ports       []int         // TCP ports to test connectivity for resolved IPs (empty = skip)
}

// Result is the final benchmark result for a single DNS server.
type Result struct {
	Name, Address      string
	Protocol           Protocol // UDP, DOT or DOH
	AvgTime            time.Duration
	SuccessRate, Score float64
	Successes, Total   int
	IsSystem           bool // whether this is the system default DNS
	Resolutions        []Resolution
	PortResults        map[string]PortResult
}

// Resolution records the IPs a DNS server resolved for a single domain.
type Resolution struct {
	Domain   string
	IPs      []string
	Category string
}

// queryResult is the raw result of a single query.
type queryResult struct {
	server   Server
	domain   string
	duration time.Duration
	ips      []string
	err      error
}

// serverStat aggregates the benchmark data for a single DNS server.
type serverStat struct {
	totalTime   time.Duration
	successes   int
	total       int
	address     string
	protocol    Protocol
	isSystem    bool
	resolutions map[string]*resolutionData
}

// resolutionData collects unique IPs resolved for a domain.
type resolutionData struct {
	ips    []string
	ipSeen map[string]struct{}
}

// ParseDomains splits, trims and deduplicates a custom domain list, preserving
// the original order. Custom domains are all assigned the CategoryCustom category.
func ParseDomains(raw string) []Domain {
	seen := make(map[string]struct{})
	var domains []Domain
	for d := range strings.SplitSeq(raw, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		domains = append(domains, Domain{Name: d, Category: CategoryCustom})
	}
	return domains
}

// Run benchmarks all servers concurrently and returns results sorted by score
// in descending order. progress is called after each completed query with that
// query's domain (may be nil); it drives the live progress UI.
func Run(opts Options, progress func(domain string)) []Result {
	servers := opts.Servers
	if len(servers) == 0 {
		servers = DefaultServers
	}
	if progress == nil {
		progress = func(string) {}
	}
	concurrency := max(opts.Concurrency, 1)

	// One goroutine per server, querying sequentially inside it, so connections
	// (DoT/DoH) are reused and we avoid firing thousands of requests at once
	// that would contend with each other and pollute the latency measurement.
	// Server-level concurrency is bounded by sem.
	totalQueries := len(servers) * len(opts.Domains) * opts.Queries
	resultsChan := make(chan queryResult, totalQueries)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, server := range servers {
		wg.Add(1)
		go func(s Server) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			benchmarkServer(s, opts, resultsChan, progress)
		}(server)
	}

	wg.Wait()
	close(resultsChan)

	stats := aggregateResults(resultsChan, opts.Domains)

	// Collect all unique resolved IPs and test TCP port connectivity (if enabled).
	var portResults map[string]PortResult
	if len(opts.Ports) > 0 {
		allIPs := collectUniqueIPs(stats)
		portResults = testPorts(allIPs, opts.Ports, opts.Timeout, opts.Concurrency)
	}

	return calculateScores(stats, opts.Domains, portResults)
}

// benchmarkServer runs all queries sequentially against a single server.
// It first does one warm-up query (excluded from the results) so that DoT/DoH
// establish their connections and the server hostname resolution is cached,
// bringing each protocol's measurement into a steady, comparable state.
func benchmarkServer(server Server, opts Options, ch chan<- queryResult, progress func(domain string)) {
	q, closeFn := newQuerier(server, opts.Timeout)
	defer closeFn()

	// Warm-up (result discarded).
	if len(opts.Domains) > 0 {
		_, _, _ = q(opts.Domains[0].Name)
	}

	for _, domain := range opts.Domains {
		for range opts.Queries {
			d, ips, err := q(domain.Name)
			ch <- queryResult{server: server, domain: domain.Name, duration: d, ips: ips, err: err}
			progress(domain.Name)
		}
	}
}

// aggregateResults collects and aggregates data from the channel.
// Resolved IPs are collected for all domains when port testing is enabled.
func aggregateResults(resultsChan <-chan queryResult, domains []Domain) map[string]*serverStat {
	serverStats := make(map[string]*serverStat)
	for result := range resultsChan {
		stats, ok := serverStats[result.server.Name]
		if !ok {
			stats = &serverStat{
				address:     result.server.Address,
				protocol:    result.server.Protocol,
				isSystem:    result.server.IsSystem,
				resolutions: make(map[string]*resolutionData),
			}
			serverStats[result.server.Name] = stats
		}
		stats.total++
		if result.err == nil {
			stats.totalTime += result.duration
			stats.successes++
			// Collect unique resolved IPs for all domains.
			if len(result.ips) > 0 {
				rd, ok := stats.resolutions[result.domain]
				if !ok {
					rd = &resolutionData{ipSeen: make(map[string]struct{})}
					stats.resolutions[result.domain] = rd
				}
				for _, ip := range result.ips {
					if _, seen := rd.ipSeen[ip]; !seen {
						rd.ipSeen[ip] = struct{}{}
						rd.ips = append(rd.ips, ip)
					}
				}
			}
		}
	}
	return serverStats
}

// collectUniqueIPs gathers all unique resolved IPs across all servers.
func collectUniqueIPs(stats map[string]*serverStat) []string {
	seen := make(map[string]struct{})
	var ips []string
	for _, s := range stats {
		for _, rd := range s.resolutions {
			for _, ip := range rd.ips {
				if _, ok := seen[ip]; !ok {
					seen[ip] = struct{}{}
					ips = append(ips, ip)
				}
			}
		}
	}
	return ips
}

// calculateScores computes the final Result list and sorts it by score in descending order.
func calculateScores(serverStats map[string]*serverStat, domains []Domain, portResults map[string]PortResult) []Result {
	catMap := make(map[string]string, len(domains))
	for _, d := range domains {
		catMap[d.Name] = d.Category
	}

	var results []Result
	for name, stats := range serverStats {
		res := Result{
			Name: name, Address: stats.address, Protocol: stats.protocol,
			Successes: stats.successes, Total: stats.total, IsSystem: stats.isSystem,
			PortResults: portResults,
		}

		if stats.successes > 0 {
			res.AvgTime = stats.totalTime / time.Duration(stats.successes)
			res.SuccessRate = float64(stats.successes) / float64(stats.total)
			latencyScore := 1.0 / res.AvgTime.Seconds()
			res.Score = latencyScore * (res.SuccessRate * res.SuccessRate)
		}

		// Build resolution records with domain category.
		for domain, rd := range stats.resolutions {
			res.Resolutions = append(res.Resolutions, Resolution{
				Domain:   domain,
				IPs:      rd.ips,
				Category: catMap[domain],
			})
		}
		slices.SortFunc(res.Resolutions, func(a, b Resolution) int {
			return cmp.Compare(a.Domain, b.Domain)
		})

		results = append(results, res)
	}

	slices.SortFunc(results, func(a, b Result) int {
		return cmp.Compare(b.Score, a.Score)
	})

	return results
}

// DomainEntry holds the best IP found for a single domain.
type DomainEntry struct {
	Domain  string
	IP      string // bare IP (no port)
	Latency time.Duration
}

// CollectBestIPs scans benchmark results and returns the lowest-latency
// reachable IP per domain, filtered by port connectivity.
func CollectBestIPs(results []Result, ports []int) map[string]*DomainEntry {
	best := make(map[string]*DomainEntry)
	for _, r := range results {
		for _, res := range r.Resolutions {
			for _, ip := range res.IPs {
				for _, port := range ports {
					key := net.JoinHostPort(ip, strconv.Itoa(port))
					pr, ok := r.PortResults[key]
					if !ok || !pr.OK {
						continue
					}
					cur, exists := best[res.Domain]
					if !exists || pr.Duration < cur.Latency ||
						(pr.Duration == cur.Latency && ip < cur.IP) {
						best[res.Domain] = &DomainEntry{
							Domain:  res.Domain,
							IP:      ip,
							Latency: pr.Duration,
						}
					}
				}
			}
		}
	}
	return best
}

// BuildDnspickBlock builds the hosts file block containing the best IP per domain.
// Entries are sorted by domain name for deterministic output.
// Returns the block bytes and the number of entries written.
func BuildDnspickBlock(best map[string]*DomainEntry) ([]byte, int) {
	now := time.Now().Format("2006-01-02 15:04:05")
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "\n# --- dnspick start %s ---\n", now)

	// 按域名排序，保证输出确定性，避免轮询模式下不必要的文件变更。
	domains := make([]string, 0, len(best))
	for d := range best {
		domains = append(domains, d)
	}
	slices.Sort(domains)

	count := 0
	for _, domain := range domains {
		entry := best[domain]
		latencyStr := entry.Latency.Round(time.Millisecond).String()
		fmt.Fprintf(&buf, "# %s latency %s %s\n", entry.IP, latencyStr, now)
		fmt.Fprintf(&buf, "%s %s\n", entry.IP, entry.Domain)
		count++
	}

	fmt.Fprintf(&buf, "# --- dnspick end ---\n")
	return buf.Bytes(), count
}
