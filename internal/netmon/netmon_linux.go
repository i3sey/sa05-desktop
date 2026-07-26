//go:build linux

package netmon

import (
	"context"
	"fmt"

	"github.com/vishvananda/netlink"
)

// linuxWatcher subscribes to the kernel's own notifications rather than polling: a
// handover is reported the moment it happens, and an idle machine costs nothing.
type linuxWatcher struct {
	changes chan struct{}
	done    chan struct{}
}

func watch(ctx context.Context) (Watcher, error) {
	watcher := &linuxWatcher{
		changes: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}

	addresses := make(chan netlink.AddrUpdate, 16)
	if err := netlink.AddrSubscribe(addresses, watcher.done); err != nil {
		close(watcher.done)
		return nil, fmt.Errorf("подписка на адреса недоступна: %w", err)
	}
	routes := make(chan netlink.RouteUpdate, 16)
	if err := netlink.RouteSubscribe(routes, watcher.done); err != nil {
		close(watcher.done)
		return nil, fmt.Errorf("подписка на маршруты недоступна: %w", err)
	}

	go func() {
		defer close(watcher.changes)
		for {
			select {
			case <-ctx.Done():
				watcher.Close()
				return
			case <-watcher.done:
				return
			case _, ok := <-addresses:
				if !ok {
					return
				}
				watcher.notify()
			case update, ok := <-routes:
				if !ok {
					return
				}
				// Only default routes matter: the tunnel's own /30 and every LAN route
				// change constantly and say nothing about reaching the server.
				if update.Dst == nil || isDefault(update.Dst.String()) {
					watcher.notify()
				}
			}
		}
	}()
	return watcher, nil
}

func isDefault(destination string) bool {
	return destination == "0.0.0.0/0" || destination == "::/0"
}

func (w *linuxWatcher) notify() {
	select {
	case w.changes <- struct{}{}:
	default:
		// A pending notification already covers this change.
	}
}

func (w *linuxWatcher) Changes() <-chan struct{} { return w.changes }

func (w *linuxWatcher) Close() {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
}
