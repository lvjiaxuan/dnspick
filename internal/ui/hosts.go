package ui

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/palemoky/dnspick/internal/dnsbench"
	"github.com/palemoky/dnspick/internal/i18n"
)

// hostsFilePath returns the OS-specific path to the system hosts file.
func hostsFilePath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

// WriteHostsFile writes the lowest-latency IP per domain to the system hosts
// file. Each entry is preceded by a comment line with the latency and timestamp.
// Requires port connectivity data; prints a notice and returns nil when absent.
func WriteHostsFile(results []dnsbench.Result, ports []int) error {
	m := i18n.L()

	if len(ports) == 0 {
		fmt.Fprint(os.Stderr, m.HostsNoData)
		return nil
	}

	// Collect all unique IPs per domain from all results, with port connectivity.
	type domainEntry struct {
		domain  string
		ip      string        // bare IP (no port)
		latency time.Duration
	}

	bestPerDomain := make(map[string]*domainEntry)

	for _, r := range results {
		for _, res := range r.Resolutions {
			for _, ip := range res.IPs {
				for _, port := range ports {
					key := net.JoinHostPort(ip, strconv.Itoa(port))
					pr, ok := r.PortResults[key]
					if !ok || !pr.OK {
						continue
					}
					cur, exists := bestPerDomain[res.Domain]
					if !exists || pr.Duration < cur.latency {
						bestPerDomain[res.Domain] = &domainEntry{
							domain:  res.Domain,
							ip:      ip,
							latency: pr.Duration,
						}
					}
				}
			}
		}
	}

	if len(bestPerDomain) == 0 {
		fmt.Fprint(os.Stderr, m.HostsNoData)
		return nil
	}

	path := hostsFilePath()
	now := time.Now().Format("2006-01-02 15:04:05")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write a section separator.
	fmt.Fprintf(f, "\n# --- dnspick start %s ---\n", now)

	count := 0
	for _, entry := range bestPerDomain {
		latencyStr := entry.latency.Round(time.Millisecond).String()
		fmt.Fprintf(f, "# %s latency %s %s\n", entry.ip, latencyStr, now)
		fmt.Fprintf(f, "%s %s\n", entry.ip, entry.domain)
		count++
	}

	fmt.Fprintf(f, "# --- dnspick end ---\n")

	fmt.Fprintf(os.Stderr, m.HostsWritten, count, path)
	return nil
}
