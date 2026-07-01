package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"golang.org/x/term"

	"github.com/lvjiaxuan/dnspick/internal/dnsbench"
	"github.com/lvjiaxuan/dnspick/internal/i18n"
)

// stdinKeyCh receives single keystrokes from stdin in raw terminal mode.
// initStdinReader starts the reader goroutine exactly once and keeps it alive
// for the entire program lifetime, avoiding goroutine leaks and stdin races
// across multiple WaitForNextRound calls.
var (
	stdinKeyCh   chan byte
	stdinKeyOnce sync.Once
)

func initStdinReader() chan byte {
	stdinKeyOnce.Do(func() {
		stdinKeyCh = make(chan byte, 4)
		go func() {
			buf := make([]byte, 1)
			for {
				n, err := os.Stdin.Read(buf)
				if n > 0 {
					stdinKeyCh <- buf[0]
				}
				if err != nil {
					return
				}
			}
		}()
	})
	return stdinKeyCh
}

// catGroup is a group of domains aggregated by category (indices point into
// StatusTracker.domains/done). category is the stable category key.
type catGroup struct {
	category string
	indices  []int
}

// StatusTracker tracks the test progress of every domain and displays it live
// as a categorized table: not-started shows "-", in-progress shows a percentage,
// done shows "✔". On a TTY it refreshes in place; on a non-TTY (pipe/CI) it
// degrades to a static table plus periodic percentages.
type StatusTracker struct {
	mu         sync.Mutex
	domains    []dnsbench.Domain
	idx        map[string]int
	done       []int
	groups     []catGroup // categories displayed side by side
	maxRows    int        // largest group size (determines table row count)
	perTotal   int        // total queries per domain = servers * queries per domain
	grand      int        // total number of queries
	completed  int
	isTTY      bool
	lines      int  // number of lines rendered last time (for in-place TTY refresh)
	lastBucket int  // non-TTY: last printed 10% bucket
	started    bool // whether Start has printed the initial snapshot (non-TTY)
	out        io.Writer
	stop       chan struct{}
	doneCh     chan struct{}
}

func NewStatusTracker(domains []dnsbench.Domain, numServers, queries int) *StatusTracker {
	idx := make(map[string]int, len(domains))
	for i, d := range domains {
		idx[d.Name] = i
	}

	// Aggregate by category, preserving first-seen order, for side-by-side display.
	var order []string
	gmap := make(map[string]*catGroup)
	for i, d := range domains {
		g, ok := gmap[d.Category]
		if !ok {
			g = &catGroup{category: d.Category}
			gmap[d.Category] = g
			order = append(order, d.Category)
		}
		g.indices = append(g.indices, i)
	}
	groups := make([]catGroup, len(order))
	maxRows := 0
	for k, name := range order {
		groups[k] = *gmap[name]
		if n := len(groups[k].indices); n > maxRows {
			maxRows = n
		}
	}

	perTotal := numServers * queries
	return &StatusTracker{
		domains:  domains,
		idx:      idx,
		done:     make([]int, len(domains)),
		groups:   groups,
		maxRows:  maxRows,
		perTotal: perTotal,
		grand:    perTotal * len(domains),
		isTTY:    term.IsTerminal(int(os.Stdout.Fd())),
		out:      color.Output,
	}
}

// Progress is called after each completed query (from multiple goroutines).
func (t *StatusTracker) Progress(domain string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if i, ok := t.idx[domain]; ok {
		t.done[i]++
	}
	t.completed++
	if !t.isTTY && t.grand > 0 {
		if bucket := t.completed * 10 / t.grand; bucket > t.lastBucket {
			t.lastBucket = bucket
			fmt.Fprintf(t.out, i18n.L().ProgressPercent, bucket*10)
		}
	}
}

// Start begins the display. On a TTY it launches a periodic refresh goroutine;
// on a non-TTY it prints the static table once.
func (t *StatusTracker) Start() {
	if !t.isTTY {
		t.printSnapshot()
		t.started = true
		return
	}
	t.draw()
	t.stop = make(chan struct{})
	t.doneCh = make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		defer close(t.doneCh)
		for {
			select {
			case <-t.stop:
				return
			case <-ticker.C:
				t.draw()
			}
		}
	}()
}

// Stop ends the display and performs a final render.
func (t *StatusTracker) Stop() {
	if !t.isTTY {
		if !t.started {
			t.printSnapshot()
		}
		return
	}
	close(t.stop)
	<-t.doneCh
	t.draw()
}

// draw redraws the whole table in place on a TTY.
func (t *StatusTracker) draw() {
	t.mu.Lock()
	lines := t.renderLocked()
	prev := t.lines
	t.lines = len(lines)
	t.mu.Unlock()

	var b strings.Builder
	if prev > 0 {
		fmt.Fprintf(&b, "\033[%dA", prev) // move cursor up prev lines
	}
	for _, ln := range lines {
		b.WriteString(ln)
		b.WriteString("\033[K\n") // clear any leftover at end of line
	}
	fmt.Fprint(t.out, b.String())
}

// printSnapshot prints the current table once on a non-TTY.
func (t *StatusTracker) printSnapshot() {
	t.mu.Lock()
	lines := t.renderLocked()
	t.mu.Unlock()
	fmt.Fprintln(t.out, strings.Join(lines, "\n"))
}

// renderLocked renders the table into a slice of lines (the caller must hold
// the lock). Categories are laid out side by side as column groups (domain |
// status) to reduce vertical height.
func (t *StatusTracker) renderLocked() []string {
	var buf bytes.Buffer
	table := tablewriter.NewWriter(&buf)

	header := make([]string, 0, len(t.groups)*2)
	for _, g := range t.groups {
		header = append(header, CategoryLabel(g.category), i18n.L().StatusCol)
	}
	table.Header(header)

	for r := range t.maxRows {
		row := make([]string, 0, len(t.groups)*2)
		for _, g := range t.groups {
			if r < len(g.indices) {
				i := g.indices[r]
				row = append(row, t.domains[i].Name, statusCell(t.done[i], t.perTotal))
			} else {
				row = append(row, "", "")
			}
		}
		table.Append(row)
	}
	table.Render()

	pct := 0
	if t.grand > 0 {
		pct = t.completed * 100 / t.grand
	}
	lines := []string{fmt.Sprintf(i18n.L().ProgressLine, pct, t.completed, t.grand)}
	lines = append(lines, strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")...)
	return lines
}

// statusCell returns colored status text based on completion.
func statusCell(done, total int) string {
	switch {
	case done <= 0:
		return color.HiBlackString("-")
	case done >= total:
		return color.GreenString("✔")
	default:
		return color.CyanString("%d%%", done*100/total)
	}
}

// PrintRoundBanner prints the polling round header with round number and timestamp.
func PrintRoundBanner(round int) {
	m := i18n.L()
	now := time.Now().Format("2006-01-02 15:04:05")
	banner := fmt.Sprintf(m.RoundBanner, round, now)
	bold := color.New(color.Bold).SprintFunc()
	fmt.Println()
	fmt.Println(bold(banner))
	fmt.Println()
}

// WaitForNextRound blocks until the interval elapses, the user presses 'u'
// (TTY only, run-now), or an interrupt signal is received. On a TTY it shows
// a live countdown that refreshes in place and listens for keystrokes via raw
// terminal mode. Returns:
//
//	1 — timer elapsed normally (caller should start the next round)
//	2 — user pressed 'u' to trigger immediately (caller should start the next round)
//	0 — interrupted (caller should exit)
func WaitForNextRound(interval time.Duration) int {
	m := i18n.L()
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	nextRunAt := time.Now().Add(interval).Format("15:04:05")

	// Set up interrupt listener.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	if !isTTY {
		// Non-TTY: print a static message and wait.
		fmt.Printf(m.NextRoundAt, formatDuration(interval), nextRunAt)
		fmt.Println()
		select {
		case <-sigCh:
			fmt.Fprintf(color.Output, m.PollStopped, 0)
			return 0
		case <-time.After(interval):
			return 1
		}
	}

	// TTY: enter raw mode to capture single keystrokes without Enter.
	// The stdin reader goroutine is started exactly once (package-level singleton)
	// and reused across all calls to avoid goroutine leaks competing for stdin.
	keyCh := initStdinReader()
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err == nil {
		defer term.Restore(int(os.Stdin.Fd()), oldState)
	}

	// TTY: live countdown with in-place refresh.
	deadline := time.Now().Add(interval)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			// Clear the countdown line.
			fmt.Fprint(color.Output, "\r\033[K")
			return 1
		}

		durStr := formatDuration(remaining.Round(time.Second))
		line := fmt.Sprintf(m.NextRoundAt, durStr, nextRunAt)
		fmt.Fprintf(color.Output, "\r\033[K%s", line)

		select {
		case <-sigCh:
			fmt.Fprintf(color.Output, "\r\033[K")
			fmt.Fprintf(color.Output, m.PollStopped, 0)
			return 0
		case k := <-keyCh:
			// 'u' or 'U' → run next round immediately.
			if k == 'u' || k == 'U' {
				fmt.Fprintf(color.Output, "\r\033[K")
				fmt.Fprint(color.Output, m.PollRunNow)
				return 2
			}
			// Ctrl+C (0x03) → exit (backup for raw mode where SIGINT may not fire).
			if k == 0x03 {
				fmt.Fprintf(color.Output, "\r\033[K")
				fmt.Fprintf(color.Output, m.PollStopped, 0)
				return 0
			}
		case <-ticker.C:
			// loop continues
		}
	}
}

// PrintPollStopped prints the polling stopped message with the total round count.
func PrintPollStopped(rounds int) {
	fmt.Fprintf(color.Output, i18n.L().PollStopped, rounds)
}

// formatDuration formats a duration as "Xm Ys" or "Ys" for the countdown display.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	if total >= 60 {
		m := total / 60
		s := total % 60
		if s > 0 {
			return fmt.Sprintf("%dm %ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", total)
}
