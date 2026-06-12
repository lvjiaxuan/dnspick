package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"

	"github.com/palemoky/dns-optimizer/internal/dnsbench"
)

var (
	domainsStr       string
	queriesPerDomain int
	queryTimeout     time.Duration
	maxConcurrency   int
)

var rootCmd = &cobra.Command{
	Use:   "dns-optimizer",
	Short: "一个跨平台的 DNS 选优工具",
	Long:  `通过对一组常用域名进行并发测试，为您的网络环境推荐最快、最稳定的DNS服务器。`,
	Run:   runBenchmark,
}

func init() {
	flags := rootCmd.PersistentFlags()
	flags.StringVarP(&domainsStr, "domains", "d", strings.Join(dnsbench.DefaultDomains, ","), "用于测试的域名列表, 以逗号分隔")
	flags.IntVarP(&queriesPerDomain, "queries", "q", 3, "每个域名的查询次数")
	flags.DurationVarP(&queryTimeout, "timeout", "t", 2*time.Second, "单次查询超时时间")
	flags.IntVarP(&maxConcurrency, "concurrency", "c", 16, "同时测试的服务器数量上限")
}

func runBenchmark(cmd *cobra.Command, args []string) {
	domains := dnsbench.ParseDomains(domainsStr)
	if len(domains) == 0 {
		fmt.Println("错误: 没有有效的测试域名。")
		os.Exit(1)
	}

	servers := dnsbench.DefaultServers
	totalQueries := len(servers) * len(domains) * queriesPerDomain

	fmt.Println("DNS 选优工具: 开始对", len(servers), "个 DNS 服务器进行综合基准测试...")
	fmt.Printf("测试域名 (%d个): %s\n", len(domains), strings.Join(domains, ", "))
	fmt.Printf("每个域名查询 %d 次, 总计 %d 次查询。\n\n", queriesPerDomain, totalQueries)

	bar := newProgressBar(totalQueries)

	results := dnsbench.Run(dnsbench.Options{
		Servers:     servers,
		Domains:     domains,
		Queries:     queriesPerDomain,
		Timeout:     queryTimeout,
		Concurrency: maxConcurrency,
	}, func() { bar.Add(1) })
	fmt.Println()

	fmt.Println("--- 综合测试结果 ---")
	printResultsTable(results)

	fmt.Println("\n--- 最佳DNS推荐 (Top 3) ---")
	printRecommendations(results)
}

func newProgressBar(total int) *progressbar.ProgressBar {
	return progressbar.NewOptions(total,
		progressbar.OptionSetWriter(color.Output),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionSetDescription("[cyan]Running queries[reset]"),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
