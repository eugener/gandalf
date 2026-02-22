// Package sseutil provides shared SSE line reading utilities for provider adapters.
package sseutil

import (
	"bufio"
	"io"
	"strings"
)

const maxLineSize = 64 * 1024 // 64KB per SSE line

// NewScanner returns a bufio.Scanner configured for reading SSE lines with
// a 64KB buffer. Each call to Scan() returns a single line (without the trailing newline).
func NewScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), maxLineSize)
	return s
}

// ParseSSELine parses a single SSE line into its event type and data payload.
// It returns ok=false for empty lines, comments, and malformed lines.
//
// SSE format:
//
//	"event: <type>"  -> event=type, data="", ok=true
//	"data: <payload>" -> event="", data=payload, ok=true
//	": comment"      -> ok=false (comment)
//	""               -> ok=false (empty)
func ParseSSELine(line string) (event, data string, ok bool) {
	if line == "" {
		return "", "", false
	}
	// SSE comments start with ':'
	if line[0] == ':' {
		return "", "", false
	}

	key, value, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}
	// Strip optional leading space after colon per SSE spec
	value = strings.TrimPrefix(value, " ")

	switch key {
	case "event":
		return value, "", true
	case "data":
		return "", value, true
	default:
		return "", "", false
	}
}
