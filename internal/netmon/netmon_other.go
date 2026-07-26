//go:build !linux

package netmon

import (
	"context"
	"time"
)

// pollInterval is the fallback cadence where the platform has no change notification.
// It is slow on purpose: the fingerprint comparison is what decides anything, and this
// only bounds how late a change is noticed.
const pollInterval = 5 * time.Second

// pollWatcher compares fingerprints on a timer. Windows has IP Helper notifications, but
// they need a callback into a Go closure from a system thread; polling is the honest
// interim, and the interface stays identical for callers.
type pollWatcher struct {
	changes chan struct{}
	done    chan struct{}
}

func watch(ctx context.Context) (Watcher, error) {
	watcher := &pollWatcher{
		changes: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go func() {
		defer close(watcher.changes)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		previous := Current()
		for {
			select {
			case <-ctx.Done():
				return
			case <-watcher.done:
				return
			case <-ticker.C:
				current := Current()
				if current == previous {
					continue
				}
				previous = current
				select {
				case watcher.changes <- struct{}{}:
				default:
				}
			}
		}
	}()
	return watcher, nil
}

func (w *pollWatcher) Changes() <-chan struct{} { return w.changes }

func (w *pollWatcher) Close() {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
}
