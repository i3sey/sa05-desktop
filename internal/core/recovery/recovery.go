// Package recovery decides what to do when the network underneath the tunnel changes.
//
// The rules are the Android client's NetworkRecoveryPolicy.kt verbatim: a changed
// network is verified before it is reconnected, and automatic reconnects are bounded so
// a permanently broken route surfaces as an error instead of a restart loop.
package recovery

// Decision is the action the runtime must take.
type Decision string

const (
	// DecisionNone means the current tunnel stays as it is.
	DecisionNone Decision = "NONE"
	// DecisionWaitForNetwork means there is no usable network at all.
	DecisionWaitForNetwork Decision = "WAIT_FOR_NETWORK"
	// DecisionVerifyRoute means the tunnel must be probed before trusting it.
	DecisionVerifyRoute Decision = "VERIFY_ROUTE"
	// DecisionReconnect means the stack must be restarted.
	DecisionReconnect Decision = "RECONNECT"
	// DecisionFail means automatic recovery is exhausted.
	DecisionFail Decision = "FAIL"
)

// MaxAutomaticAttempts bounds silent reconnects before the user is told.
const MaxAutomaticAttempts = 2

// NetworkChanged compares the previous and the current network identity. An empty
// current key means the machine is offline.
func NetworkChanged(previousKey, currentKey string) Decision {
	switch {
	case currentKey == "":
		return DecisionWaitForNetwork
	case previousKey == "" || previousKey == currentKey:
		return DecisionNone
	default:
		return DecisionVerifyRoute
	}
}

// RouteChecked turns a health probe result into the next action.
func RouteChecked(healthy bool, automaticAttempts int) Decision {
	switch {
	case healthy:
		return DecisionNone
	case automaticAttempts < MaxAutomaticAttempts:
		return DecisionReconnect
	default:
		return DecisionFail
	}
}
