package dispatch

import (
	"context"
	"time"

	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

// keepDelegationAlive refreshes the board until execution has finished or the context is canceled.
func (d *Dispatcher) keepDelegationAlive(ctx context.Context, taskID string) func() {
	return runHeartbeat(ctx, board.PoolHeartbeatInterval, func() {
		_ = d.Board.TouchDelegation(taskID)
	})
}

func runHeartbeat(ctx context.Context, interval time.Duration, touch func()) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				touch()
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
