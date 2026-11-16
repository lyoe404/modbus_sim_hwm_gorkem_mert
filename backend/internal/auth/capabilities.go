package auth

// Capability strings. One per read surface and one per control action; flat,
// so granting one never implies another (matches the brief's catalog).
const (
	ReadSensor       = "read:sensor"
	ReadRelay        = "read:relay"
	ReadThermocouple = "read:thermocouple"
	ReadFanSpeed     = "read:fan_speed"
	ReadEvents       = "read:events"
	ReadDeviceStatus = "read:device_status"

	WriteRelay      = "write:relay"
	WriteFanSpeed   = "write:fan_speed"
	WriteThresholds = "write:thresholds"
	WriteReset      = "write:reset"
)

// Baseline roles. A role is only shorthand for a set of capabilities; routes
// never look at the role, only at the capabilities in the token.
const (
	RoleViewer   = "viewer"
	RoleOperator = "operator"
)

var readCaps = []string{ReadSensor, ReadRelay, ReadThermocouple, ReadFanSpeed, ReadEvents, ReadDeviceStatus}
var writeCaps = []string{WriteRelay, WriteFanSpeed, WriteThresholds, WriteReset}

// PermsForRole expands a role into its capability list.
func PermsForRole(role string) ([]string, bool) {
	switch role {
	case RoleViewer:
		return append([]string(nil), readCaps...), true
	case RoleOperator:
		return append(append([]string(nil), readCaps...), writeCaps...), true
	}
	return nil, false
}
