// Package temphumidity holds the simulated Temperature & Humidity Sensor state and Modbus handler.
package temphumidity

// Register map (Modbus TCP Port 5021, FC4 Read Input Registers):
//   Input reg 0 : Temperature, °C x 10 (bounded random walk, 18.0..35.0 °C)
//   Input reg 1 : Humidity, % x 10 (bounded random walk, 30.0..80.0 %)
const (
	AddrTemp     uint16 = 0
	AddrHumidity uint16 = 1
	InputRegs           = 2

	TempMin     = 18.0
	TempMax     = 35.0
	HumidityMin = 30.0
	HumidityMax = 80.0

	GarbageValue uint16 = 0xFFFF
)
