package app

import (
	"context"
	"time"

	"github.com/fife/sa05-desktop/internal/core/state"
)

// trafficInterval is how often the counters are sampled. One second is what makes a rate
// readable: shorter jitters, longer feels frozen.
const trafficInterval = time.Second

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
			}
		}
	}()
}

// stopTrafficMeter halts sampling and clears the figures, so a disconnected client does
// not keep showing the last rate it saw.
func (a *App) stopTrafficMeter() {
	a.mutex.Lock()
	stop := a.trafficStop
	a.trafficStop = nil
	a.mutex.Unlock()
	if stop != nil {
		stop()
	}
}
