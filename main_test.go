package main

import (
	"testing"
	"time"
)

func TestLangFromArgs(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"--version"}, ""},
		{[]string{"--lang", "zh"}, "zh"},
		{[]string{"--lang=zh"}, "zh"},
		{[]string{"--lang=en"}, "en"},
		{[]string{"-d", "example.com", "--lang", "zh"}, "zh"},
		{[]string{"--lang"}, ""}, // --lang without value
	}
	for _, tt := range tests {
		if got := langFromArgs(tt.args); got != tt.want {
			t.Errorf("langFromArgs(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestParsePorts(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"", nil},
		{"  ", nil},
		{"443", []int{443}},
		{"443,22", []int{443, 22}},
		{" 443 , 22 , 80 ", []int{443, 22, 80}},
		{"443,443,22", []int{443, 22}},  // deduplicate
		{"abc,443,xyz", []int{443}},      // invalid entries skipped
		{"0,443,70000", []int{443}},      // out-of-range skipped
		{",,,443,,,", []int{443}},        // empty segments skipped
	}
	for _, tt := range tests {
		got := parsePorts(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parsePorts(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parsePorts(%q)[%d] = %d, want %d", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestParseInterval(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"", 0},
		{"  ", 0},
		{"0", 0},
		{"abc", 0},
		{"-1", 0},
		{"1", 1 * time.Minute},
		{"5", 5 * time.Minute},
		{" 10 ", 10 * time.Minute},
	}
	for _, tt := range tests {
		got := parseInterval(tt.input)
		if got != tt.want {
			t.Errorf("parseInterval(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
