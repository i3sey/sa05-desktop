package app

import (
	"context"
	"errors"

	"github.com/fife/sa05-desktop/internal/core/diag"
	"github.com/fife/sa05-desktop/internal/core/state"
)

// diagnosticTargets is the standard set unless a caller replaced it; tests point it at a
// local server so they exercise the wiring without depending on public sites.
func (a *App) diagnosticTargets() []diag.Target {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if len(a.diagTargets) > 0 {
		return a.diagTargets
	}
	return diag.Targets
}

// DiagnosticsReport is the whole answer to "why doesn't it work".
type DiagnosticsReport struct {
	Verdict diag.Verdict  `json:"verdict"`
	Results []diag.Result `json:"results"`
	// ThroughTunnel says which network the probes used, because the same failure means
	// opposite things depending on the answer.
	ThroughTunnel bool `json:"throughTunnel"`
}

// Diagnose probes the standard targets and returns a verdict.
//
// The probes go through the tunnel when one is up and directly otherwise: a site that
// fails both ways is down or blocked by address, while a site that works directly and
// fails through the tunnel points at the tunnel.
func (a *App) Diagnose(ctx context.Context) (DiagnosticsReport, error) {
	snapshot := a.states.Snapshot()
	runner := &diag.Runner{}
	if snapshot.Status == state.StatusConnected && snapshot.SocksPort > 0 {
		runner.SocksPort = snapshot.SocksPort
	}

	results := runner.Run(ctx, a.diagnosticTargets(), nil)
	if len(results) == 0 {
		return DiagnosticsReport{}, errors.New("Проверка не выполнена")
	}
	return DiagnosticsReport{
		Verdict:       diag.Describe(results),
		Results:       results,
		ThroughTunnel: runner.SocksPort != 0,
	}, nil
}
