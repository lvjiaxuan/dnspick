// Package dnsbench provides a concurrent benchmarking engine for DNS servers:
// concurrent queries, connection reuse, result aggregation and scoring. It
// contains no command-line or terminal presentation logic.
package dnsbench

import (
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// Protocol identifies the DNS transport used by a server.
type Protocol string

// Supported protocols.
const (
	UDP  Protocol = "udp"
	DOT  Protocol = "dot"
	DOH  Protocol = "doh"
	DOH3 Protocol = "doh3" // DNS-over-HTTPS carried over HTTP/3 (QUIC)
)

// Server describes a DNS server to be tested.
type Server struct {
	Name     string
	Address  string
	Protocol Protocol
	IsSystem bool // whether this is the detected system default DNS
}

// Domain categories. These are stable internal keys; use CategoryLabel for
// localized display text.
const (
	CategoryDomestic = "domestic"
	CategoryForeign  = "foreign"
	CategoryCustom   = "custom"
)

// Domain is a test domain with its category.
type Domain struct {
	Name, Category string
}

// ParseServers parses a comma-separated custom server list, inferring each
// server's protocol from its URL scheme, preserving input order and skipping
// duplicates:
//
//	h3://host/path    -> DoH3 (rewritten to https:// for the request)
//	https://host/path -> DoH
//	tls://host        -> DoT (scheme stripped; host used for TLS SNI)
//	host or IP        -> UDP
//
// Entries that cannot be parsed are skipped.
func ParseServers(raw string) []Server {
	seen := make(map[string]struct{})
	var servers []Server
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, ok := seen[entry]; ok {
			continue
		}
		seen[entry] = struct{}{}
		if s, ok := parseServer(entry); ok {
			servers = append(servers, s)
		}
	}
	return servers
}

// parseServer turns a single user-supplied entry into a Server.
func parseServer(entry string) (Server, bool) {
	switch {
	case strings.HasPrefix(entry, "h3://"):
		// HTTP/3 transport requires an https URL; rewrite the scheme but keep
		// the rest of the endpoint intact.
		addr := "https://" + strings.TrimPrefix(entry, "h3://")
		return Server{Name: customName(hostOf(addr), DOH3), Address: addr, Protocol: DOH3}, true

	case strings.HasPrefix(entry, "https://"):
		return Server{Name: customName(hostOf(entry), DOH), Address: entry, Protocol: DOH}, true

	case strings.HasPrefix(entry, "tls://"):
		host := strings.TrimPrefix(entry, "tls://")
		return Server{Name: customName(host, DOT), Address: host, Protocol: DOT}, true

	case strings.HasPrefix(entry, "udp://"):
		host := strings.TrimPrefix(entry, "udp://")
		return Server{Name: customName(host, UDP), Address: host, Protocol: UDP}, true

	default:
		return Server{Name: customName(entry, UDP), Address: entry, Protocol: UDP}, true
	}
}

// hostOf extracts the host portion of an https URL, falling back to the raw
// string when it cannot be parsed.
func hostOf(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	return rawURL
}

// customName builds a display name for a user-supplied server, e.g.
// "dns.google (DoT)".
func customName(host string, p Protocol) string {
	label := map[Protocol]string{UDP: "UDP", DOT: "DoT", DOH: "DoH", DOH3: "DoH3"}[p]
	return host + " (" + label + ")"
}

// configFile mirrors the top-level structure of dnspick-config.yml.
type configFile struct {
	Servers []struct {
		Name     string `yaml:"name"`
		Address  string `yaml:"address"`
		Protocol string `yaml:"protocol"`
	} `yaml:"servers"`
	Domains []struct {
		Name     string `yaml:"name"`
		Category string `yaml:"category"`
	} `yaml:"domains"`
}

// DefaultServers is populated from dnspick-config.yml via LoadConfig.
var DefaultServers []Server

// DefaultDomains is populated from dnspick-config.yml via LoadConfig.
var DefaultDomains []Domain

// LoadConfig parses the given YAML bytes and populates DefaultServers and
// DefaultDomains. It should be called once at program startup.
func LoadConfig(data []byte) error {
	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	DefaultServers = make([]Server, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		DefaultServers = append(DefaultServers, Server{
			Name:     s.Name,
			Address:  s.Address,
			Protocol: Protocol(s.Protocol),
		})
	}

	DefaultDomains = make([]Domain, 0, len(cfg.Domains))
	for _, d := range cfg.Domains {
		DefaultDomains = append(DefaultDomains, Domain{
			Name:     d.Name,
			Category: d.Category,
		})
	}

	return nil
}
