// Package dnsbench provides a concurrent benchmarking engine for DNS servers:
// concurrent queries, connection reuse, result aggregation and scoring. It
// contains no command-line or terminal presentation logic.
package dnsbench

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Protocol identifies the DNS transport used by a server.
type Protocol string

// Supported protocols.
const (
	UDP Protocol = "udp"
	DOT Protocol = "dot"
	DOH Protocol = "doh"
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

// configFile mirrors the top-level structure of configs.yml.
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

// DefaultServers is populated from configs.yml via LoadConfig.
var DefaultServers []Server

// DefaultDomains is populated from configs.yml via LoadConfig.
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
