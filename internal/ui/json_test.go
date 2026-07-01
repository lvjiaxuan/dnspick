package ui

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/lvjiaxuan/dnspick/internal/dnsbench"
)

func TestWriteJSON(t *testing.T) {
	results := []dnsbench.Result{
		{
			Name: "Fast", Address: "1.1.1.1", Protocol: dnsbench.UDP,
			AvgTime: 10 * time.Millisecond, SuccessRate: 1.0,
			Successes: 6, Total: 6, Score: 100,
		},
		{
			Name: "Slow", Address: "2.2.2.2", Protocol: dnsbench.UDP,
			AvgTime: 50 * time.Millisecond, SuccessRate: 0.8,
			Successes: 4, Total: 5, Score: 10, IsSystem: true,
		},
	}

	var buf bytes.Buffer
	if err := WriteJSON(&buf, results, 3, 2, nil, 0, ""); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	// Verify the output is valid JSON and contains expected fields.
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}

	// Schema version.
	if v, ok := doc["schema"].(float64); !ok || int(v) != jsonSchemaVersion {
		t.Errorf("schema = %v, want %d", doc["schema"], jsonSchemaVersion)
	}

	// Number of results.
	arr, ok := doc["results"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("results = %v, want 2-element array", doc["results"])
	}

	// First result should be ranked 1.
	first := arr[0].(map[string]any)
	if first["name"] != "Fast" {
		t.Errorf("first result name = %v, want Fast", first["name"])
	}
	if first["rank"].(float64) != 1 {
		t.Errorf("first result rank = %v, want 1", first["rank"])
	}

	// Recommendation should have system_dns since one result is IsSystem.
	rec := doc["recommendation"].(map[string]any)
	if rec["system_dns"] == nil {
		t.Error("expected system_dns in recommendation")
	}
}

func TestLatencyMs(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want float64
	}{
		{10 * time.Millisecond, 10.0},
		{1500 * time.Microsecond, 1.5},
		{0, 0},
	}
	for _, tt := range tests {
		if got := latencyMs(tt.d); got != tt.want {
			t.Errorf("latencyMs(%v) = %v, want %v", tt.d, got, tt.want)
		}
	}
}

func TestWriteJSON_WithResolutionsAndPorts(t *testing.T) {
	portResults := map[string]dnsbench.PortResult{
		"10.0.0.1:443": {Port: 443, Duration: 50 * time.Millisecond, OK: true},
		"10.0.0.2:443": {Port: 443, Duration: 0, OK: false},
	}
	results := []dnsbench.Result{
		{
			Name: "Fast", Address: "1.1.1.1", Protocol: dnsbench.UDP,
			AvgTime: 10 * time.Millisecond, SuccessRate: 1.0,
			Successes: 3, Total: 3, Score: 100,
			Resolutions: []dnsbench.Resolution{
				{Domain: "example.com", IPs: []string{"10.0.0.1"}, Category: "foreign"},
			},
			PortResults: portResults,
		},
		{
			Name: "Slow", Address: "2.2.2.2", Protocol: dnsbench.UDP,
			AvgTime: 50 * time.Millisecond, SuccessRate: 1.0,
			Successes: 3, Total: 3, Score: 10, IsSystem: true,
			Resolutions: []dnsbench.Resolution{
				{Domain: "example.com", IPs: []string{"10.0.0.2"}, Category: "foreign"},
			},
			PortResults: portResults,
		},
	}

	var buf bytes.Buffer
	if err := WriteJSON(&buf, results, 3, 1, []int{443}, 0, ""); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}

	// Schema version should be 3.
	if v, ok := doc["schema"].(float64); !ok || int(v) != jsonSchemaVersion {
		t.Errorf("schema = %v, want %d", doc["schema"], jsonSchemaVersion)
	}

	// ports should be present.
	portsArr, ok := doc["ports"].([]any)
	if !ok || len(portsArr) != 1 {
		t.Fatalf("ports = %v, want [443]", doc["ports"])
	}

	// First result should have resolutions.
	arr := doc["results"].([]any)
	first := arr[0].(map[string]any)
	resArr, ok := first["resolutions"].([]any)
	if !ok || len(resArr) != 1 {
		t.Fatalf("resolutions = %v, want 1-element array", first["resolutions"])
	}
	res := resArr[0].(map[string]any)
	if res["domain"] != "example.com" {
		t.Errorf("resolution domain = %v, want example.com", res["domain"])
	}

	// port_results should be present with 2 entries.
	prArr, ok := doc["port_results"].([]any)
	if !ok || len(prArr) != 2 {
		t.Fatalf("port_results = %v, want 2-element array", doc["port_results"])
	}
	// First port result should be OK with latency.
	pr := prArr[0].(map[string]any)
	if pr["ok"] != true {
		t.Errorf("first port_result ok = %v, want true", pr["ok"])
	}
	if pr["latency_ms"] == nil {
		t.Error("first port_result should have latency_ms")
	}
	// Second port result should not be OK.
	pr2 := prArr[1].(map[string]any)
	if pr2["ok"] != false {
		t.Errorf("second port_result ok = %v, want false", pr2["ok"])
	}
}
