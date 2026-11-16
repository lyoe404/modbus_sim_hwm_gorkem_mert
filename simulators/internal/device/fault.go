package device

import (
	"fmt"
	"time"
)

// FaultMode selects how the simulator misbehaves.
type FaultMode string

const (
	FaultNone    FaultMode = "none"    // normal operation
	FaultSlow    FaultMode = "slow"    // answers correctly, but after Delay
	FaultTimeout FaultMode = "timeout" // stalls for Delay, then fails (client times out first)
	FaultError   FaultMode = "error"   // immediate Modbus exception (server device failure)
	FaultGarbage FaultMode = "garbage" // answers with out-of-range values (0xFFFF)
	FaultDown    FaultMode = "down"    // listener closed; connection refused (handled in main)
)

// DefaultFaultDelay is used when a fault is set without an explicit delay.
const DefaultFaultDelay = 10 * time.Second

// ParseFaultMode validates a user-supplied mode string.
func ParseFaultMode(s string) (FaultMode, error) {
	switch m := FaultMode(s); m {
	case FaultNone, FaultSlow, FaultTimeout, FaultError, FaultGarbage, FaultDown:
		return m, nil
	}
	return "", fmt.Errorf("unknown fault mode %q (none|slow|timeout|error|garbage|down)", s)
}
