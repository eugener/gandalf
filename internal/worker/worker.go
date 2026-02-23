// Package worker provides background task infrastructure for the gateway.
package worker

import "context"

// Worker is a long-running background task.
type Worker interface {
	// Run blocks until ctx is cancelled or an unrecoverable error occurs.
	Run(ctx context.Context) error
}
