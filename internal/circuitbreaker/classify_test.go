package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
)

// statusError implements httpStatusError for testing.
type statusError struct {
	code int
}

func (e *statusError) Error() string       { return fmt.Sprintf("HTTP %d", e.code) }
func (e *statusError) HTTPStatus() int     { return e.code }

func TestClassifyError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want float64
	}{
		{"nil", nil, 0},
		{"429", &statusError{429}, 0.5},
		{"500", &statusError{500}, 1.0},
		{"502", &statusError{502}, 1.0},
		{"503", &statusError{503}, 1.0},
		{"504", &statusError{504}, 1.0},
		{"400", &statusError{400}, 0.0},
		{"401", &statusError{401}, 0.0},
		{"403", &statusError{403}, 0.0},
		{"404", &statusError{404}, 0.0},
		{"context_deadline", context.DeadlineExceeded, 1.5},
		{"os_deadline", os.ErrDeadlineExceeded, 1.5},
		{"wrapped_deadline", fmt.Errorf("wrap: %w", context.DeadlineExceeded), 1.5},
		{"network_error", &net.OpError{Op: "dial", Err: errors.New("refused")}, 1.0},
		{"generic_error", errors.New("something broke"), 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyError(tt.err)
			if got != tt.want {
				t.Errorf("ClassifyError(%v) = %f, want %f", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyError_WrappedStatus(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("provider: %w", &statusError{502})
	if got := ClassifyError(wrapped); got != 1.0 {
		t.Errorf("wrapped 502 = %f, want 1.0", got)
	}
}
