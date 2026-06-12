package dnsbench

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// startTestDNS 启动一个本地 UDP DNS 服务器，按 handler 应答，返回监听地址。
func startTestDNS(t *testing.T, handler dns.HandlerFunc) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: handler}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return pc.LocalAddr().String()
}

func TestReusableExchange(t *testing.T) {
	var calls atomic.Int32
	addr := startTestDNS(t, func(w dns.ResponseWriter, r *dns.Msg) {
		calls.Add(1)
		m := new(dns.Msg)
		m.SetReply(r)
		rr, _ := dns.NewRR(r.Question[0].Name + " 60 IN A 1.2.3.4")
		m.Answer = append(m.Answer, rr)
		w.WriteMsg(m)
	})

	client := &dns.Client{Net: "udp", Timeout: time.Second}
	q, closeFn := reusableExchange(client, addr)
	defer closeFn()

	for i := range 3 {
		d, err := q("example.com")
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		if d <= 0 {
			t.Fatalf("query %d: non-positive duration %v", i, d)
		}
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("server received %d queries, want 3", got)
	}
}

func TestReusableExchangeServfail(t *testing.T) {
	addr := startTestDNS(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeServerFailure
		w.WriteMsg(m)
	})

	client := &dns.Client{Net: "udp", Timeout: time.Second}
	q, closeFn := reusableExchange(client, addr)
	defer closeFn()

	if _, err := q("example.com"); err == nil {
		t.Fatal("expected error for SERVFAIL rcode")
	}
}

func TestDohQuery(t *testing.T) {
	client := &http.Client{Timeout: time.Second}

	t.Run("valid", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"Status":0,"Answer":[{"data":"1.2.3.4"}]}`))
		}))
		defer srv.Close()
		if err := dohQuery(client, srv.URL, "example.com"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("nxdomain status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"Status":3}`))
		}))
		defer srv.Close()
		if err := dohQuery(client, srv.URL, "example.com"); err == nil {
			t.Fatal("expected error for non-zero status")
		}
	})

	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		if err := dohQuery(client, srv.URL, "example.com"); err == nil {
			t.Fatal("expected error for HTTP 500")
		}
	})

	t.Run("garbage body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`not json`))
		}))
		defer srv.Close()
		if err := dohQuery(client, srv.URL, "example.com"); err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}
