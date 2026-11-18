package fault

import (
	"fmt"
	"time"
)

// Mode selects how a simulator misbehaves during fault-injection tests.
type Mode string

const (
	None      Mode = "none"      // normal operation
	Slow      Mode = "slow"      // answers correctly, but after Delay
	Timeout   Mode = "timeout"   // stalls for Delay, then returns server device failure
	Error     Mode = "error"     // immediate Modbus exception (0x04 Server Device Failure)
	Garbage   Mode = "garbage"   // returns sentinel/out-of-range values (0xFFFF / 0x8000 / inverted coils)
	Down      Mode = "down"      // listener closed; connection refused (handled by process supervisor)
	Malformed Mode = "malformed" // truncated response PDU (temperature/humidity simulator)
)

const DefaultDelay = 10 * time.Second

func ParseMode(s string, extra ...Mode) (Mode, error) {
	switch m := Mode(s); m {
	case None, Slow, Timeout, Error, Garbage, Down:
		return m, nil
	}
	modes := "none|slow|timeout|error|garbage|down"
	for _, m := range extra {
		if s == string(m) {
			return m, nil
		}
		modes += "|" + string(m)
	}
	return "", fmt.Errorf("unknown fault mode %q (%s)", s, modes)
}
