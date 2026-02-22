package sseutil

import (
	"strings"
	"testing"
)

func TestParseSSELine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		line      string
		wantEvent string
		wantData  string
		wantOK    bool
	}{
		{name: "data line", line: `data: {"id":"1"}`, wantData: `{"id":"1"}`, wantOK: true},
		{name: "event line", line: "event: message_start", wantEvent: "message_start", wantOK: true},
		{name: "data done", line: "data: [DONE]", wantData: "[DONE]", wantOK: true},
		{name: "empty line", line: "", wantOK: false},
		{name: "comment", line: ": keep-alive", wantOK: false},
		{name: "no colon", line: "garbage", wantOK: false},
		{name: "data no space", line: `data:{"id":"1"}`, wantData: `{"id":"1"}`, wantOK: true},
		{name: "unknown field", line: "retry: 5000", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			event, data, ok := ParseSSELine(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if event != tt.wantEvent {
				t.Errorf("event = %q, want %q", event, tt.wantEvent)
			}
			if data != tt.wantData {
				t.Errorf("data = %q, want %q", data, tt.wantData)
			}
		})
	}
}

func TestNewScanner(t *testing.T) {
	t.Parallel()

	input := "data: line1\ndata: line2\n\n"
	s := NewScanner(strings.NewReader(input))

	var lines []string
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	if len(lines) != 3 { // "data: line1", "data: line2", ""
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0] != "data: line1" {
		t.Errorf("line[0] = %q, want %q", lines[0], "data: line1")
	}
}
