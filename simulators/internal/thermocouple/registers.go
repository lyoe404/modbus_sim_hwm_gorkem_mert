// Package thermocouple holds the simulated Multi-Channel Thermocouple Module state and Modbus handler.
package thermocouple

// Register map (Modbus TCP Port 5023, FC4 Read Input Registers):
//   Input reg 0..3 : Thermocouple Channel 0..3 Temperatures, °C x 10 (e.g. 0..800 °C)
//   Sentinel 0x8000: Open-circuit / channel fault indicator
const (
	ChannelCount = 4
	InputRegs    = ChannelCount

	TempMin = 40.0
	TempMax = 350.0

	ChannelFaultSentinel uint16 = 0x8000
	GarbageValue         uint16 = 0xFFFF
)
