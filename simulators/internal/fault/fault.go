package fault

import (
	"fmt"
	"time"
)

// Mode selects how a simulator misbehaves during fault-injection tests.
type Mode string

const (
	None    Mode = "none"    // normal operation
	Slow    Mode = "slow"    // answers correctly, but after Delay
	Timeout Mode = "timeout" // stalls for Delay, then returns server device failure
	Error   Mode = "error"   // immediate Modbus exception (0x04 Server Device Failure)
	Garbage Mode = "garbage" // returns sentinel/out-of-range values (0xFFFF / 0x8000 / inverted coils)
	Down    Mode = "down"    // listener closed; connection refused (handled by process supervisor)
)

const DefaultDelay = 10 * time.Second

func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case None, Slow, Timeout, Error, Garbage, Down:
		return m, nil
	}
	return "", fmt.Errorf("unknown fault mode %q (none|slow|timeout|error|garbage|down)", s)
}
