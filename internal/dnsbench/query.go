package dnsbench

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// querier performs a single query for a domain and returns how long it took
// along with any resolved A-record IP addresses.
type querier func(domain string) (time.Duration, []string, error)

// newQuerier builds a reusable query function and its cleanup function for a
// server. The server hostname is resolved to an IP up front so that the system
// DNS resolution time is not counted in the measurement.
func newQuerier(server Server, timeout time.Duration) (querier, func()) {
	switch server.Protocol {
	case UDP:
		ip := resolveHost(server.Address, timeout)
		client := &dns.Client{Net: "udp", Timeout: timeout}
		return reusableExchange(client, net.JoinHostPort(ip, "53"))

	case DOT:
		ip := resolveHost(server.Address, timeout)
		client := &dns.Client{
			Net:       "tcp-tls",
			Timeout:   timeout,
			TLSConfig: &tls.Config{ServerName: server.Address},
		}
		return reusableExchange(client, net.JoinHostPort(ip, "853"))

	case DOH:
		client := &http.Client{Timeout: timeout}
		q := func(domain string) (time.Duration, []string, error) {
			start := time.Now()
			msg, err := dohQuery(client, server.Address, domain)
			elapsed := time.Since(start)
			if err != nil {
				return elapsed, nil, err
			}
			return elapsed, extractIPs(msg), nil
		}
		return q, client.CloseIdleConnections

	default:
		q := func(domain string) (time.Duration, []string, error) {
			return 0, nil, fmt.Errorf("unsupported protocol: %s", server.Protocol)
		}
		return q, func() {}
	}
}

// reusableExchange maintains a persistent connection (a UDP socket or DoT TLS
// connection) reused across queries, so each measurement reflects a single
// query round-trip rather than a fresh handshake every time. A broken
// connection is reconnected and retried once. A querier is used sequentially
// within a single goroutine, so no locking is needed.
func reusableExchange(client *dns.Client, addr string) (querier, func()) {
	var conn *dns.Conn

	exchange := func(m *dns.Msg) (*dns.Msg, error) {
		if conn == nil {
			c, err := client.Dial(addr)
			if err != nil {
				return nil, err
			}
			conn = c
		}
		r, _, err := client.ExchangeWithConn(m, conn)
		return r, err
	}

	query := func(domain string) (time.Duration, []string, error) {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(domain), dns.TypeA)

		start := time.Now()
		r, err := exchange(m)
		if err != nil {
			// The connection may have been closed by the peer; drop it and retry once.
			if conn != nil {
				conn.Close()
				conn = nil
			}
			start = time.Now() // reset so the measurement reflects only the retry
			r, err = exchange(m)
		}
		elapsed := time.Since(start)

		if err != nil {
			if conn != nil {
				conn.Close()
				conn = nil
			}
			return elapsed, nil, err
		}
		if r.Rcode != dns.RcodeSuccess {
			return elapsed, nil, fmt.Errorf("DNS response code %s", dns.RcodeToString[r.Rcode])
		}
		return elapsed, extractIPs(r), nil
	}

	closeFn := func() {
		if conn != nil {
			conn.Close()
			conn = nil
		}
	}
	return query, closeFn
}

// dohQuery sends a single DoH query per RFC 8484 in wire-format
// (application/dns-message) and returns the parsed DNS message so the caller
// can extract resolved IP addresses. Unlike the inconsistent JSON dialects
// across vendors, wire-format is the DoH standard and is supported by every
// server on the /dns-query endpoint.
func dohQuery(client *http.Client, endpoint, domain string) (*dns.Msg, error) {
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	wire, err := q.Pack()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(wire))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024)) // drain so the connection can be reused
		return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	var r dns.Msg
	if err := r.Unpack(body); err != nil {
		return nil, fmt.Errorf("failed to parse DoH response: %w", err)
	}
	if r.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("DNS response code %s", dns.RcodeToString[r.Rcode])
	}
	return &r, nil
}

// extractIPs returns the IPv4 addresses from A records in a DNS response.
func extractIPs(r *dns.Msg) []string {
	if r == nil {
		return nil
	}
	var ips []string
	for _, rr := range r.Answer {
		if a, ok := rr.(*dns.A); ok {
			ips = append(ips, a.A.String())
		}
	}
	return ips
}

// PortResult holds the TCP port connectivity test result for a single IP and port.
type PortResult struct {
	Port     int
	Duration time.Duration
	OK       bool
}

// testPorts concurrently tests TCP connectivity on the given ports for all
// provided IPs. It uses a semaphore to bound concurrency and returns a map
// keyed by "ip:port" → result.
func testPorts(ips []string, ports []int, timeout time.Duration, concurrency int) map[string]PortResult {
	if len(ips) == 0 || len(ports) == 0 {
		return nil
	}
	results := make(map[string]PortResult, len(ips)*len(ports))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(concurrency, 1))

	for _, ip := range ips {
		for _, port := range ports {
			wg.Add(1)
			go func(ip string, port int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				start := time.Now()
				conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(port)), timeout)
				d := time.Since(start)
				if err == nil {
					conn.Close()
				}
				key := net.JoinHostPort(ip, strconv.Itoa(port))
				mu.Lock()
				results[key] = PortResult{Port: port, Duration: d, OK: err == nil}
				mu.Unlock()
			}(ip, port)
		}
	}
	wg.Wait()
	return results
}

// resolveHost resolves a hostname to an IP address, preferring IPv4; if it is
// already an IP or resolution fails, it is returned unchanged.
func resolveHost(host string, timeout time.Duration) string {
	if net.ParseIP(host) != nil {
		return host
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		return host
	}
	// Prefer an IPv4 address so the benchmark works on IPv4-only networks.
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return a
		}
	}
	return addrs[0]
}
