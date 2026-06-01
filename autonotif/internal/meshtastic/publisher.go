package meshtastic

import (
	"context"
	"log/slog"
	"time"
)

// NodeInfoPublisher is the minimal interface required to publish node info
// periodically. It is satisfied by *Client.
type NodeInfoPublisher interface {
	PublishNodeInfo() error
}

// StartNodeInfoPublisher publishes node info on a fixed interval until the
// context is cancelled. It returns immediately and runs in a background
// goroutine. interval <= 0 disables periodic publishing.
func StartNodeInfoPublisher(ctx context.Context, publisher NodeInfoPublisher, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := publisher.PublishNodeInfo(); err != nil {
					slog.Warn("node info publish failed", "error", err)
				} else {
					slog.Info("node info published")
				}
			}
		}
	}()
}
