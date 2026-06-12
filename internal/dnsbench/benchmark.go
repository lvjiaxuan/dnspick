package dnsbench

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Options 控制一次基准测试的行为。
type Options struct {
	Servers     []Server      // 待测试的服务器；为空时使用 DefaultServers
	Domains     []string      // 测试域名
	Queries     int           // 每个域名的查询次数
	Timeout     time.Duration // 单次查询超时
	Concurrency int           // 同时测试的服务器数量上限
}

// Result 是单个 DNS 服务器的最终测试结果。
type Result struct {
	Name, Address      string
	AvgTime            time.Duration
	SuccessRate, Score float64
	Successes, Total   int
}

// queryResult 是单次查询的原始结果。
type queryResult struct {
	server   Server
	duration time.Duration
	err      error
}

// serverStat 聚合单个 DNS 服务器的测试数据。
type serverStat struct {
	totalTime time.Duration
	successes int
	total     int
	address   string
}

// ParseDomains 拆分、去空格并去重域名列表，保持原始顺序。
func ParseDomains(raw string) []string {
	seen := make(map[string]struct{})
	var domains []string
	for d := range strings.SplitSeq(raw, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		domains = append(domains, d)
	}
	return domains
}

// Run 并发对所有服务器执行基准测试并返回按评分降序排列的结果。
// progress 在每次查询完成后被调用（可为 nil），用于驱动进度条等 UI。
func Run(opts Options, progress func()) []Result {
	servers := opts.Servers
	if len(servers) == 0 {
		servers = DefaultServers
	}
	if progress == nil {
		progress = func() {}
	}
	concurrency := max(opts.Concurrency, 1)

	// 每个服务器一个 goroutine，内部顺序查询，从而复用连接（DoT/DoH）并
	// 避免成千上万个请求同时打出导致互相争抢、污染延迟测量。
	// 服务器级别的并发由 sem 限制。
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

	return calculateScores(aggregateResults(resultsChan))
}

// benchmarkServer 对单个服务器顺序执行所有查询。
// 先做一次不计入结果的预热查询，让 DoT/DoH 建立连接、缓存服务器域名解析，
// 使各协议的测量进入稳定状态、更具可比性。
func benchmarkServer(server Server, opts Options, ch chan<- queryResult, progress func()) {
	q, closeFn := newQuerier(server, opts.Timeout)
	defer closeFn()

	// 预热（结果丢弃）。
	_, _ = q(opts.Domains[0])

	for _, domain := range opts.Domains {
		for range opts.Queries {
			d, err := q(domain)
			ch <- queryResult{server: server, duration: d, err: err}
			progress()
		}
	}
}

// aggregateResults 负责从 channel 收集并聚合数据。
func aggregateResults(resultsChan <-chan queryResult) map[string]*serverStat {
	serverStats := make(map[string]*serverStat)
	for result := range resultsChan {
		stats, ok := serverStats[result.server.Name]
		if !ok {
			stats = &serverStat{address: result.server.Address}
			serverStats[result.server.Name] = stats
		}
		stats.total++
		if result.err == nil {
			stats.totalTime += result.duration
			stats.successes++
		}
	}
	return serverStats
}

// calculateScores 计算最终的 Result 列表并按评分降序排序。
func calculateScores(serverStats map[string]*serverStat) []Result {
	var results []Result
	for name, stats := range serverStats {
		res := Result{
			Name: name, Address: stats.address, Successes: stats.successes, Total: stats.total,
		}

		if stats.successes > 0 {
			res.AvgTime = stats.totalTime / time.Duration(stats.successes)
			res.SuccessRate = float64(stats.successes) / float64(stats.total)
			latencyScore := 1.0 / res.AvgTime.Seconds()
			res.Score = latencyScore * (res.SuccessRate * res.SuccessRate)
		}
		results = append(results, res)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}
