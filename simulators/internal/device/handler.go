package device

import (
	"time"

	"github.com/simonvetter/modbus"
)

// Handler implements modbus.RequestHandler on top of a Store, applying the
// active fault mode to every request.
type Handler struct{ s *Store }

// NewHandler wraps a store.
func NewHandler(s *Store) *Handler { return &Handler{s: s} }

// gate applies the fault mode. A non-nil error must be returned to the client.
func (h *Handler) gate() (FaultMode, error) {
	mode, delay := h.s.Fault()
	switch mode {
	case FaultSlow:
		time.Sleep(delay)
	case FaultTimeout:
		time.Sleep(delay)
		return mode, modbus.ErrServerDeviceFailure
	case FaultError:
		return mode, modbus.ErrServerDeviceFailure
	}
	return mode, nil
}

// HandleCoils serves FC1 (read), FC5 and FC15 (write).
func (h *Handler) HandleCoils(req *modbus.CoilsRequest) ([]bool, error) {
	mode, err := h.gate()
	if err != nil {
		return nil, err
	}
	if req.IsWrite {
		if !h.s.WriteCoils(req.Addr, req.Args) {
			return nil, modbus.ErrIllegalDataAddress
		}
		return nil, nil
	}
	vals, ok := h.s.Coils(req.Addr, req.Quantity)
	if !ok {
		return nil, modbus.ErrIllegalDataAddress
	}
	if mode == FaultGarbage {
		for i := range vals {
			vals[i] = !vals[i] // lie about every coil
		}
	}
	return vals, nil
}

// HandleInputRegisters serves FC4.
func (h *Handler) HandleInputRegisters(req *modbus.InputRegistersRequest) ([]uint16, error) {
	mode, err := h.gate()
	if err != nil {
		return nil, err
	}
	vals, ok := h.s.InputRegisters(req.Addr, req.Quantity)
	if !ok {
		return nil, modbus.ErrIllegalDataAddress
	}
	if mode == FaultGarbage {
		for i := range vals {
			vals[i] = GarbageValue
		}
	}
	return vals, nil
}

// HandleHoldingRegisters is not used yet (speed module is Extended scope).
func (h *Handler) HandleHoldingRegisters(*modbus.HoldingRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

// HandleDiscreteInputs is not used by this device.
func (h *Handler) HandleDiscreteInputs(*modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}
