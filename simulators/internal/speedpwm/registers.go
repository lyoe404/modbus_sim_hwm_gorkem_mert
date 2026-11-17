// Package speedpwm holds the simulated Speed Control PWM Module state and Modbus handler.
package speedpwm

// Register map (Modbus TCP Port 5024, Holding Registers FC3/FC6/FC16):
//   Holding reg 0 : Target PWM Duty / Speed (0..100 %)
//   Holding reg 1 : Measured Tachometer RPM (derived from duty cycle, e.g. 0..3000 RPM)
const (
	RegTargetPWM   uint16 = 0
	RegMeasuredRPM uint16 = 1
	HoldingRegs           = 2

	MaxPWMPercent uint16 = 100
	MaxRPM        uint16 = 3000
	GarbageValue  uint16 = 0xFFFF
)
