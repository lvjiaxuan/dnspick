package dnsbench

import (
	"errors"
	"testing"
	"time"
)

func TestParseDomains(t *testing.T) {
	got := ParseDomains(" a.com , b.com ,a.com,, c.com ")
	want := []string{"a.com", "b.com", "c.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
		if got[i].Category != CategoryCustom {
			t.Fatalf("domain %q category = %q, want %q", got[i].Name, got[i].Category, CategoryCustom)
		}
	}
}

func TestParseDomainsEmpty(t *testing.T) {
	if got := ParseDomains("  , , "); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestAggregateResults(t *testing.T) {
	srv := Server{Name: "X", Address: "1.2.3.4", Protocol: UDP}
	ch := make(chan queryResult, 3)
	ch <- queryResult{server: srv, duration: 10 * time.Millisecond}
	ch <- queryResult{server: srv, duration: 30 * time.Millisecond}
	ch <- queryResult{server: srv, err: errors.New("boom")}
	close(ch)

	stats := aggregateResults(ch, nil)
	s, ok := stats["X"]
	if !ok {
		t.Fatal("missing server X")
	}
	if s.total != 3 || s.successes != 2 {
		t.Fatalf("total=%d successes=%d, want 3/2", s.total, s.successes)
	}
	if s.totalTime != 40*time.Millisecond {
		t.Fatalf("totalTime=%v, want 40ms", s.totalTime)
	}
	if s.address != "1.2.3.4" {
		t.Fatalf("address=%q", s.address)
	}
}

func TestCalculateScores(t *testing.T) {
	stats := map[string]*serverStat{
		"fast":  {totalTime: 20 * time.Millisecond, successes: 2, total: 2, address: "1.1.1.1"},
		"slow":  {totalTime: 200 * time.Millisecond, successes: 2, total: 2, address: "2.2.2.2"},
		"flaky": {totalTime: 10 * time.Millisecond, successes: 1, total: 2, address: "3.3.3.3"},
		"dead":  {successes: 0, total: 2, address: "4.4.4.4"},
	}

	res := calculateScores(stats, nil, nil)
	if len(res) != 4 {
		t.Fatalf("expected 4 results, got %d", len(res))
	}
	// After sorting, results should be in descending score order.
	for i := 1; i < len(res); i++ {
		if res[i-1].Score < res[i].Score {
			t.Fatalf("results not sorted by score desc: %+v", res)
		}
	}
	if res[0].Name != "fast" {
		t.Fatalf("expected fast first, got %q", res[0].Name)
	}
	if res[len(res)-1].Name != "dead" {
		t.Fatalf("expected dead last, got %q", res[len(res)-1].Name)
	}

	// dead server: no successes, so score and avg latency should be 0.
	dead := res[len(res)-1]
	if dead.Score != 0 || dead.AvgTime != 0 || dead.SuccessRate != 0 {
		t.Fatalf("dead server should be all-zero, got %+v", dead)
	}

	// fast's avg latency should be 10ms (20ms / 2 successes).
	if res[0].AvgTime != 10*time.Millisecond {
		t.Fatalf("fast AvgTime=%v, want 10ms", res[0].AvgTime)
	}
}

func TestAggregateResultsIPCollection(t *testing.T) {
	srv := Server{Name: "X", Address: "1.2.3.4", Protocol: UDP}
	domains := []Domain{
		{Name: "a.com", Category: CategoryForeign},
		{Name: "b.com", Category: CategoryForeign},
	}

	ch := make(chan queryResult, 4)
	ch <- queryResult{server: srv, domain: "a.com", duration: 10 * time.Millisecond, ips: []string{"1.1.1.1"}}
	ch <- queryResult{server: srv, domain: "a.com", duration: 20 * time.Millisecond, ips: []string{"1.1.1.1", "2.2.2.2"}}
	ch <- queryResult{server: srv, domain: "b.com", duration: 15 * time.Millisecond, ips: []string{"3.3.3.3"}}
	ch <- queryResult{server: srv, domain: "b.com", duration: 25 * time.Millisecond, ips: []string{"4.4.4.4"}}
	close(ch)

	stats := aggregateResults(ch, domains)
	s := stats["X"]

	// Both domains should have resolutions collected (no per-domain filter).
	if _, ok := s.resolutions["a.com"]; !ok {
		t.Fatal("expected a.com to have resolutions collected")
	}
	if _, ok := s.resolutions["b.com"]; !ok {
		t.Fatal("expected b.com to have resolutions collected")
	}

	// Verify a.com has unique IPs.
	aIPs := s.resolutions["a.com"].ips
	if len(aIPs) != 2 {
		t.Fatalf("expected 2 unique IPs for a.com, got %d: %v", len(aIPs), aIPs)
	}

	// total/successes should still count all queries.
	if s.total != 4 || s.successes != 4 {
		t.Fatalf("total=%d successes=%d, want 4/4", s.total, s.successes)
	}
}
