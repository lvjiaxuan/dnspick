package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
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

// stripDnspickBlocks removes all previously written dnspick sections from
// the given hosts file content. A dnspick section starts with a line
// containing "# --- dnspick start" and ends with "# --- dnspick end ---".
// The leading blank line before a start marker (if any) is also removed.
func stripDnspickBlocks(content []byte) []byte {
	var buf bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(content))
	inBlock := false
	for scanner.Scan() {
		line := scanner.Text()
		if !inBlock && strings.HasPrefix(line, "# --- dnspick start") {
			inBlock = true
			// Trim the trailing blank line we may have just written.
			b := buf.Bytes()
			if len(b) > 0 && b[len(b)-1] == '\n' {
				// Check if the last line is blank.
				lastNL := bytes.LastIndexByte(b[:len(b)-1], '\n')
				if lastNL >= 0 && bytes.TrimSpace(b[lastNL+1:len(b)-1]) == nil {
					buf.Truncate(lastNL + 1)
				} else if bytes.TrimSpace(b) == nil {
					buf.Reset()
				}
			}
			continue
		}
		if inBlock {
			if strings.HasPrefix(line, "# --- dnspick end") {
				inBlock = false
			}
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
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

	// Read existing content and strip old dnspick blocks.
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	cleaned := stripDnspickBlocks(existing)

	// Build the new dnspick block.
	var block bytes.Buffer
	fmt.Fprintf(&block, "\n# --- dnspick start %s ---\n", now)

	count := 0
	for _, entry := range bestPerDomain {
		latencyStr := entry.latency.Round(time.Millisecond).String()
		fmt.Fprintf(&block, "# %s latency %s %s\n", entry.ip, latencyStr, now)
		fmt.Fprintf(&block, "%s %s\n", entry.ip, entry.domain)
		count++
	}

	fmt.Fprintf(&block, "# --- dnspick end ---\n")

	// Write cleaned content + new block atomically.
	var out bytes.Buffer
	out.Write(cleaned)
	out.Write(block.Bytes())

	if err := os.WriteFile(path, out.Bytes(), 0644); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, m.HostsWritten, count, path)
	return nil
}
