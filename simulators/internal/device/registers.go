// Package device holds the simulated device state and its Modbus handler.
package device

// Register map (single source of truth; keep README in sync).
//
//  Input reg 0   Temperature, °C x 10
//  Input reg 1   Humidity, % x 10
//  Input reg 2   Thermocouple Temperature, °C x 10
//  Holding reg 0 PWM Fan Speed, 0..100%
//  Coil 0..7     Relay channels 0..7 (read FC1, write FC5/FC15)
const (
	AddrTemp         uint16 = 0
	AddrHumidity     uint16 = 1
	AddrThermocouple uint16 = 2
	InputRegs               = 3

	HoldingRegFanSpeed uint16 = 0
	HoldingRegs               = 1

	CoilCount = 8

	TempMin     = 18.0
	TempMax     = 35.0
	HumidityMin = 30.0
	HumidityMax = 80.0

	// GarbageValue is returned for every register in FaultGarbage mode.
	GarbageValue uint16 = 0xFFFF
)