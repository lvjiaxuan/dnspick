package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
	"golang.org/x/term"

	"github.com/lvjiaxuan/dnspick/internal/buildinfo"
	"github.com/lvjiaxuan/dnspick/internal/console"
	"github.com/lvjiaxuan/dnspick/internal/dnsbench"
	"github.com/lvjiaxuan/dnspick/internal/i18n"
	"github.com/lvjiaxuan/dnspick/internal/ui"
	"github.com/lvjiaxuan/dnspick/internal/updater"
)

//go:embed dnspicker-config.yml
var embeddedConfig []byte

var (
	domainsStr       string
	serversStr       string
	queriesPerDomain int
	queryTimeout     time.Duration
	maxConcurrency   int
	noSystemDNS      bool
	langFlag         string
	jsonOutput       bool
	portStr          string
	portOnlyStr      string
	writeHosts       bool
	intervalStr      string
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

	// Cobra's Windows "mousetrap" otherwise intercepts a double-click launch,
	// prints "This is a command line tool..." and exits before the command
	// runs. dnspick is meant to be usable by double-clicking, and the console
	// is kept open afterwards by console.PauseOnExit, so disable the mousetrap.
	cobra.MousetrapHelpText = ""

	rootCmd.Short = m.CmdRootShort
	rootCmd.Long = m.CmdRootLong
	versionCmd.Short = m.CmdVersionShort
	updateCmd.Short = m.CmdUpdateShort

	rootCmd.SetVersionTemplate("{{.Version}}\n")

	flags := rootCmd.PersistentFlags()
	flags.StringVarP(&domainsStr, "domains", "d", "", m.FlagDomains)
	flags.StringVarP(&serversStr, "servers", "s", "", m.FlagServers)
	flags.IntVarP(&queriesPerDomain, "queries", "q", 3, m.FlagQueries)
	flags.DurationVarP(&queryTimeout, "timeout", "t", 2*time.Second, m.FlagTimeout)
	flags.IntVarP(&maxConcurrency, "concurrency", "c", 16, m.FlagConcurrency)
	flags.BoolVar(&noSystemDNS, "no-system-dns", false, m.FlagNoSystemDNS)
	flags.StringVar(&langFlag, "lang", "", m.FlagLang)
	flags.BoolVar(&jsonOutput, "json", false, m.FlagJSON)
	flags.StringVar(&portStr, "port", "", m.FlagPort)
	flags.StringVar(&portOnlyStr, "port-only", "", m.FlagPortOnly)
	flags.BoolVarP(&writeHosts, "write", "w", false, m.FlagWrite)
	flags.StringVar(&intervalStr, "interval", "", m.FlagInterval)

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

	// Parse polling interval (0 or empty = single run).
	interval := parseInterval(intervalStr)

	// Domains: use the custom list when -d is given (classified as Custom),
	// otherwise fall back to the built-in categorized list.
	domains := dnsbench.DefaultDomains
	if cmd.Flags().Changed("domains") {
		domains = dnsbench.ParseDomains(domainsStr)
	}
	if len(domains) == 0 {
		return fmt.Errorf("%s", m.ErrNoDomains)
	}

	// Servers: the custom list when -s is given, otherwise the built-in list;
	// in both cases the system default DNS is appended unless disabled.
	servers := dnsbench.DefaultServers
	if cmd.Flags().Changed("servers") {
		servers = dnsbench.ParseServers(serversStr)
		if len(servers) == 0 {
			return fmt.Errorf("%s", m.ErrNoServers)
		}
	}
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

	// Single-run mode: no loop.
	if interval <= 0 {
		return executeOnce(opts, ports, portOnly, 0, "")
	}

	// Polling mode: loop until interrupted.
	round := 0
	for {
		round++
		if !jsonOutput {
			ui.PrintRoundBanner(round)
		}

		now := time.Now()
		if err := executeOnce(opts, ports, portOnly, round, now.Format(time.RFC3339)); err != nil {
			return err
		}

		result := ui.WaitForNextRound(interval)
		if result == 0 {
			// Interrupted: print summary and exit cleanly.
			ui.PrintPollStopped(round)
			return nil
		}
	}
}

// executeOnce runs a single benchmark cycle. round and timestamp are included
// in JSON output when in polling mode (round > 0); they are ignored for
// human-readable output.
func executeOnce(opts dnsbench.Options, ports []int, portOnly bool, round int, timestamp string) error {
	m := i18n.L()

	// JSON mode: stdout carries only the JSON document, status goes to stderr,
	// and the live progress UI is skipped so the output stays pipe-friendly.
	if jsonOutput {
		// 在 benchmark 开始前清理旧的 hosts 条目并刷新 DNS 缓存。
		if writeHosts {
			if err := ui.ClearOldEntries(); err != nil {
				fmt.Fprintf(os.Stderr, m.HostsFailed, err)
			}
			ui.FlushDNSCache()
		}
		fmt.Fprintf(os.Stderr, m.BenchStarting, len(opts.Servers), len(opts.Domains))
		results := dnsbench.Run(opts, nil)
		// JSON 模式下同样需要将最佳 IP 写入 hosts 文件。
		if writeHosts {
			if err := ui.WriteHostsFile(results, ports); err != nil {
				fmt.Fprintf(os.Stderr, m.HostsFailed, err)
			}
		}
		return ui.WriteJSON(os.Stdout, results, queriesPerDomain, len(opts.Domains), ports, round, timestamp)
	}

	// 在 benchmark 开始前清理旧的 hosts 条目并刷新 DNS 缓存，避免旧 IP 影响测试结果。
	if writeHosts {
		if err := ui.ClearOldEntries(); err != nil {
			fmt.Fprintf(os.Stderr, m.HostsFailed, err)
		}
		ui.FlushDNSCache()
	}

	// Kick off a non-blocking check for a newer release; it runs concurrently
	// with the benchmark and the notice (if any) is printed at the end.
	updateCh := startUpdateCheck()

	fmt.Printf(m.BenchStarting, len(opts.Servers), len(opts.Domains))

	tracker := ui.NewStatusTracker(opts.Domains, len(opts.Servers), queriesPerDomain)
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
		ui.FlushDNSCache()
	}

	autoUpdate(updateCh)
	return nil
}

// updateCheckTimeout bounds the background "is there a newer release?" check so a
// slow or unreachable network never holds anything up for long.
const updateCheckTimeout = 3 * time.Second

// updateNoticeGrace is how long the final notice waits for a still-pending check
// before giving up, so an unusually fast benchmark doesn't block on the network.
const updateNoticeGrace = 1500 * time.Millisecond

// startUpdateCheck launches a non-blocking check for a newer release and returns
// a channel that yields the result (or nil on any error). It is skipped for
// non-release builds (e.g. "dev"), which are never valid semver, so local builds
// are not nagged on every run.
func startUpdateCheck() <-chan *updater.CheckResult {
	ch := make(chan *updater.CheckResult, 1)
	if !semver.IsValid(buildinfo.Version) {
		ch <- nil
		return ch
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
		defer cancel()
		res, err := updater.Check(ctx, buildinfo.Version)
		if err != nil {
			ch <- nil
			return
		}
		ch <- res
	}()
	return ch
}

// autoUpdate acts on the background update check. When a newer release is found
// it prints a notice and updates in place automatically. In a non-interactive
// context (piped/CI) it does not self-modify, printing a passive hint instead so
// scripted runs stay reproducible. It waits at most updateNoticeGrace for a
// still-pending check; a pending or failed check does nothing.
func autoUpdate(ch <-chan *updater.CheckResult) {
	var res *updater.CheckResult
	select {
	case res = <-ch:
	case <-time.After(updateNoticeGrace):
		return // check still running; don't block this run
	}
	if res == nil || !res.HasUpdate {
		return
	}

	m := i18n.L()
	// Non-interactive (piped/CI): just hint, never self-modify unprompted.
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Printf(m.UpdateAvailable, res.Current, res.Latest, res.URL)
		return
	}

	fmt.Printf(m.UpdateAutoNotice, res.Current, res.Latest)
	ctx, cancel := context.WithTimeout(context.Background(), updater.DefaultTimeout)
	defer cancel()
	latest, updated, err := updater.Update(ctx, res.Current)
	if err != nil {
		fmt.Printf("%s %v\n", m.UpdateFailed, err)
		return
	}
	if updated {
		fmt.Printf(m.UpdateDone, latest)
	}
}

func main() {
	// Load servers/domains from ~/dnspick-config.yml, fallback to embedded dnspicker-config.yml.
	var configData []byte
	if homeDir, err := os.UserHomeDir(); err == nil {
		if data, err := os.ReadFile(filepath.Join(homeDir, "dnspick-config.yml")); err == nil {
			configData = data
		}
	}
	if configData == nil {
		configData = embeddedConfig
	}
	if err := dnsbench.LoadConfig(configData); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// Resolve the language before building commands so that help text honors
	// --lang. Cobra renders help without running PreRun hooks, so the flag is
	// scanned manually here from the raw arguments.
	i18n.Set(i18n.Detect(langFromArgs(os.Args[1:])))
	setup()

	err := rootCmd.Execute()
	if err != nil {
		fmt.Println(err)
	}

	// On Windows a double-click (or a launcher like Listary) gives the process
	// its own console that closes the moment it exits, so the user never sees
	// the results. Pause in that case, but not when --json is piped somewhere.
	if !jsonOutput {
		console.PauseOnExit()
	}

	if err != nil {
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

// parseInterval parses the --interval flag value as minutes and returns a
// time.Duration. Returns 0 for empty or invalid values (single-run mode).
// The minimum effective interval is 1 minute.
func parseInterval(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Minute
}
