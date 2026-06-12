package dnsbench

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/miekg/dns"
)

// querier 执行一次对某域名的查询并返回耗时。
type querier func(domain string) (time.Duration, error)

// newQuerier 为某个服务器构造一个可复用的查询函数及其清理函数。
// 服务器主机名在此处预先解析为 IP，避免把系统 DNS 的解析耗时计入测量。
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
		q := func(domain string) (time.Duration, error) {
			start := time.Now()
			err := dohQuery(client, server.Address, domain)
			return time.Since(start), err
		}
		return q, client.CloseIdleConnections

	default:
		q := func(domain string) (time.Duration, error) {
			return 0, fmt.Errorf("不支持的协议: %s", server.Protocol)
		}
		return q, func() {}
	}
}

// reusableExchange 维护一条持久连接（UDP socket 或 DoT 的 TLS 连接），
// 在多次查询间复用，使各次测量只反映单次查询往返而非每次重新握手。
// 连接失效时会自动重连重试一次。同一 querier 仅在单个 goroutine 中顺序使用，无需加锁。
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

	query := func(domain string) (time.Duration, error) {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(domain), dns.TypeA)

		start := time.Now()
		r, err := exchange(m)
		if err != nil {
			// 连接可能已被对端关闭，丢弃后重连重试一次。
			if conn != nil {
				conn.Close()
				conn = nil
			}
			r, err = exchange(m)
		}
		elapsed := time.Since(start)

		if err != nil {
			if conn != nil {
				conn.Close()
				conn = nil
			}
			return elapsed, err
		}
		if r.Rcode != dns.RcodeSuccess {
			return elapsed, fmt.Errorf("DNS 响应码 %s", dns.RcodeToString[r.Rcode])
		}
		return elapsed, nil
	}

	closeFn := func() {
		if conn != nil {
			conn.Close()
			conn = nil
		}
	}
	return query, closeFn
}

// dohResponse 是 application/dns-json 响应的最小子集（RFC 8484 风格）。
type dohResponse struct {
	Status int `json:"Status"`
}

// dohQuery 发起一次 DoH 查询并校验返回内容（而不仅仅是 HTTP 状态码）。
func dohQuery(client *http.Client, endpoint, domain string) error {
	reqURL := fmt.Sprintf("%s?name=%s&type=A", endpoint, domain)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/dns-json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) // 读完以便连接复用
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	var parsed dohResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("解析 DoH 响应失败: %w", err)
	}
	if parsed.Status != 0 {
		return fmt.Errorf("DoH 响应状态码 %d", parsed.Status)
	}
	return nil
}

// resolveHost 把主机名解析为 IP；若本身已是 IP 或解析失败，则原样返回。
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
	return addrs[0]
}
