package app

import (
	"context"
	"log"
	"time"

	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/storage"
)

// trafficInterval is how often the counters are sampled. One second is what makes a rate
// readable: shorter jitters, longer feels frozen.
const trafficInterval = time.Second

// usageFlushInterval is how often session deltas are folded into the persisted
// day/month accumulators. Every second would rewrite the state file holding the
// subscription tokens; every minute is plenty for "today" figures.
const usageFlushInterval = time.Minute

// startTrafficMeter publishes byte totals and the current rate while the tunnel is up.
//
// The numbers come from the core's own counters rather than from a wrapper of our own:
// the core is the only place that sees every connection it routed.
func (a *App) startTrafficMeter(ctx context.Context) {
	meterCtx, cancel := context.WithCancel(ctx)

	a.mutex.Lock()
	if a.trafficStop != nil {
		a.trafficStop()
	}
	a.trafficStop = cancel
	current := a.core.Traffic()
	a.usageBaseUp, a.usageBaseDown = current.Uplink, current.Downlink
	a.usageFlushedAt = time.Now()
	a.mutex.Unlock()

	go func() {
		ticker := time.NewTicker(trafficInterval)
		defer ticker.Stop()

		previous := a.core.Traffic()
		previousAt := time.Now()
		for {
			select {
			case <-meterCtx.Done():
				return
			case now := <-ticker.C:
				current := a.core.Traffic()
				seconds := now.Sub(previousAt).Seconds()
				if seconds <= 0 {
					continue
				}
				// A restarted core resets its counters; a negative delta means exactly
				// that, and reporting it as a rate would show nonsense.
				upDelta := current.Uplink - previous.Uplink
				downDelta := current.Downlink - previous.Downlink
				if upDelta < 0 || downDelta < 0 {
					upDelta, downDelta = 0, 0
				}
				previous, previousAt = current, now

				a.states.Update(func(next *state.Snapshot) {
					next.TrafficUp = current.Uplink
					next.TrafficDown = current.Downlink
					next.RateUp = int64(float64(upDelta) / seconds)
					next.RateDown = int64(float64(downDelta) / seconds)
				})

				a.mutex.Lock()
				due := now.Sub(a.usageFlushedAt) >= usageFlushInterval
				a.mutex.Unlock()
				if due {
					a.flushUsage()
				}
			}
		}
	}()
}

// flushUsage folds the session traffic since the last flush into the persisted day/month
// accumulators. A core restart resets its counters, so a totals regression means the
// whole current total is new traffic.
func (a *App) flushUsage() {
	current := a.core.Traffic()
	a.mutex.Lock()
	upDelta := current.Uplink - a.usageBaseUp
	downDelta := current.Downlink - a.usageBaseDown
	if upDelta < 0 {
		upDelta = current.Uplink
	}
	if downDelta < 0 {
		downDelta = current.Downlink
	}
	a.usageBaseUp, a.usageBaseDown = current.Uplink, current.Downlink
	a.usageFlushedAt = time.Now()
	a.mutex.Unlock()

	if upDelta == 0 && downDelta == 0 {
		return
	}
	if _, err := a.store.Update(func(next *storage.State) {
		next.Usage = storage.AddUsage(next.Usage, time.Now(), upDelta, downDelta)
	}); err != nil {
		log.Printf("трафик дня не сохранён: %v", err)
	}
}

// stopTrafficMeter halts sampling and clears the figures, so a disconnected client does
// not keep showing the last rate it saw. The tail since the last minute flush is folded
// into the accumulators first, while the core is still alive to be read.
func (a *App) stopTrafficMeter() {
	a.mutex.Lock()
	stop := a.trafficStop
	a.trafficStop = nil
	a.mutex.Unlock()
	if stop != nil {
		stop()
		a.flushUsage()
	}
}
