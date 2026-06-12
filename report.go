package main

import (
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"

	"github.com/palemoky/dns-optimizer/internal/dnsbench"
)

// printResultsTable 使用 tablewriter 打印漂亮的结果表格。
func printResultsTable(results []dnsbench.Result) {
	table := tablewriter.NewWriter(os.Stdout)
	table.Header([]string{"DNS服务器", "地址", "平均延迟", "成功率", "综合评分"})

	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()

	for _, r := range results {
		rateStr := fmt.Sprintf("%.1f%% (%d/%d)", r.SuccessRate*100, r.Successes, r.Total)
		if r.SuccessRate < 1.0 {
			rateStr = red(rateStr)
		} else {
			rateStr = green(rateStr)
		}

		table.Append([]string{
			r.Name,
			r.Address,
			r.AvgTime.Round(time.Microsecond).String(),
			rateStr,
			fmt.Sprintf("%.2f", r.Score),
		})
	}
	table.Render()
}

// printRecommendations 打印 Top 推荐。
func printRecommendations(results []dnsbench.Result) {
	palette := []*color.Color{
		color.New(color.FgGreen, color.Bold),
		color.New(color.FgYellow),
		color.New(color.FgCyan),
	}
	red := color.New(color.FgRed)

	found := 0
	for _, best := range results {
		if best.SuccessRate <= 0.98 {
			continue
		}
		palette[found].Printf("#%d: %s (%s)\n", found+1, best.Name, best.Address)
		fmt.Printf("    综合评分: %.2f, 平均延迟: %s, 成功率: %.1f%%\n",
			best.Score, best.AvgTime.Round(time.Microsecond).String(), best.SuccessRate*100)
		found++
		if found >= len(palette) {
			break
		}
	}
	if found == 0 {
		red.Println("没有找到表现足够好的DNS服务器，请检查网络连接。")
	}
}
