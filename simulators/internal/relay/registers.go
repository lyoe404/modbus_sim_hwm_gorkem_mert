// Package relay holds the simulated 8-Channel Relay Card state and Modbus handler.
package relay

// Register map (Modbus TCP Port 5022):
//   Coil 0..7 : 8 Relay channels (Read FC1, Write Single FC5, Write Multiple FC15)
const (
	CoilCount = 8
)
