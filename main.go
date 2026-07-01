package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/palemoky/dnspick/internal/buildinfo"
	"github.com/palemoky/dnspick/internal/dnsbench"
	"github.com/palemoky/dnspick/internal/i18n"
	"github.com/palemoky/dnspick/internal/ui"
	"github.com/palemoky/dnspick/internal/updater"
)

//go:embed configs.yml
var embeddedConfig []byte

var (
	domainsStr       string
	queriesPerDomain int
	queryTimeout     time.Duration
	maxConcurrency   int
	noSystemDNS      bool
	langFlag         string
	jsonOutput       bool
	portStr          string
	portOnlyStr      string
	writeHosts       bool
)

var rootCmd = &cobra.Command{
	Use:           "dnspick",
	Version:       buildinfo.Version,
	RunE:          runBenchmark,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var versionCmd = &cobra.Command{
	Use: "version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(buildinfo.String())
	},
}

var updateCmd = &cobra.Command{
	Use:  "update",
	RunE: runUpdate,
}

// setup wires up localized help text, flags and subcommands. It must run after
// the active language has been selected so that --help reflects --lang/$LANG.
func setup() {
	m := i18n.L()

	rootCmd.Short = m.CmdRootShort
	rootCmd.Long = m.CmdRootLong
	versionCmd.Short = m.CmdVersionShort
	updateCmd.Short = m.CmdUpdateShort

	rootCmd.SetVersionTemplate("{{.Version}}\n")

	flags := rootCmd.PersistentFlags()
	flags.StringVarP(&domainsStr, "domains", "d", "", m.FlagDomains)
	flags.IntVarP(&queriesPerDomain, "queries", "q", 3, m.FlagQueries)
	flags.DurationVarP(&queryTimeout, "timeout", "t", 2*time.Second, m.FlagTimeout)
	flags.IntVarP(&maxConcurrency, "concurrency", "c", 16, m.FlagConcurrency)
	flags.BoolVar(&noSystemDNS, "no-system-dns", false, m.FlagNoSystemDNS)
	flags.StringVar(&langFlag, "lang", "", m.FlagLang)
	flags.BoolVar(&jsonOutput, "json", false, m.FlagJSON)
	flags.StringVar(&portStr, "port", "", m.FlagPort)
	flags.StringVar(&portOnlyStr, "port-only", "", m.FlagPortOnly)
	flags.BoolVarP(&writeHosts, "write", "w", false, m.FlagWrite)

	rootCmd.AddCommand(versionCmd, updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), updater.DefaultTimeout)
	defer cancel()

	m := i18n.L()
	fmt.Printf(m.UpdateChecking, buildinfo.Version)
	latest, updated, err := updater.Update(ctx, buildinfo.Version)
	if err != nil {
		return fmt.Errorf("%s %w", m.UpdateFailed, err)
	}
	if !updated {
		fmt.Printf(m.UpdateUpToDate, latest)
		return nil
	}
	fmt.Printf(m.UpdateDone, latest)
	return nil
}

func runBenchmark(cmd *cobra.Command, args []string) error {
	m := i18n.L()

	// Parse port lists: --port-only implies --port and skips benchmark output.
	portOnly := cmd.Flags().Changed("port-only")
	ports := parsePorts(portStr)
	if portOnly {
		ports = parsePorts(portOnlyStr)
	}

	// Domains: use the custom list when -d is given (classified as Custom),
	// otherwise fall back to the built-in categorized list.
	domains := dnsbench.DefaultDomains
	if cmd.Flags().Changed("domains") {
		domains = dnsbench.ParseDomains(domainsStr)
	}
	if len(domains) == 0 {
		return fmt.Errorf("%s", m.ErrNoDomains)
	}

	// Servers: built-in list + (unless disabled) the system default DNS.
	servers := dnsbench.DefaultServers
	if !noSystemDNS {
		if sys := dnsbench.DetectSystemDNS(m.SystemDNSName, m.SystemDNSNameN); len(sys) > 0 {
			servers = append(append([]dnsbench.Server{}, servers...), sys...)
		}
	}

	opts := dnsbench.Options{
		Servers:     servers,
		Domains:     domains,
		Queries:     queriesPerDomain,
		Timeout:     queryTimeout,
		Concurrency: maxConcurrency,
		Ports:       ports,
	}

	// JSON mode: stdout carries only the JSON document, status goes to stderr,
	// and the live progress UI is skipped so the output stays pipe-friendly.
	if jsonOutput {
		fmt.Fprintf(os.Stderr, m.BenchStarting, len(servers), len(domains))
		results := dnsbench.Run(opts, nil)
		return ui.WriteJSON(os.Stdout, results, queriesPerDomain, len(domains), ports)
	}

	fmt.Printf(m.BenchStarting, len(servers), len(domains))

	tracker := ui.NewStatusTracker(domains, len(servers), queriesPerDomain)
	tracker.Start()
	results := dnsbench.Run(opts, tracker.Progress)
	tracker.Stop()

	// If --port-only is set, skip the benchmark results and recommendations.
	if !portOnly {
		fmt.Println(m.ResultsHeader)
		ui.PrintResultsTable(results)

		fmt.Println(m.RecommendHeader)
		ui.PrintRecommendations(results)
	}

	ui.PrintResolutions(results, ports)

	if writeHosts {
		if err := ui.WriteHostsFile(results, ports); err != nil {
			fmt.Fprintf(os.Stderr, m.HostsFailed, err)
		}
	}

	return nil
}

func main() {
	// Load servers/domains from configs.yml (embedded at compile time).
	if err := dnsbench.LoadConfig(embeddedConfig); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// Resolve the language before building commands so that help text honors
	// --lang. Cobra renders help without running PreRun hooks, so the flag is
	// scanned manually here from the raw arguments.
	i18n.Set(i18n.Detect(langFromArgs(os.Args[1:])))
	setup()

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// langFromArgs extracts the value of --lang from raw CLI arguments, supporting
// both "--lang zh" and "--lang=zh" forms. Returns "" when absent.
func langFromArgs(args []string) string {
	for i, a := range args {
		switch {
		case a == "--lang":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "--lang="):
			return strings.TrimPrefix(a, "--lang=")
		}
	}
	return ""
}

// parsePorts splits a comma-separated port list, parses each as an integer,
// deduplicates, and returns them. Invalid entries are silently skipped.
func parsePorts(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	seen := make(map[int]struct{})
	var ports []int
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		p, err := strconv.Atoi(tok)
		if err != nil || p <= 0 || p > 65535 {
			continue
		}
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			ports = append(ports, p)
		}
	}
	return ports
}
