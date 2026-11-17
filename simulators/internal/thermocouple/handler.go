package thermocouple

import (
	"time"

	"github.com/simonvetter/modbus"
	"sim/internal/fault"
)

type Handler struct{ s *Store }

func NewHandler(s *Store) *Handler { return &Handler{s: s} }

func (h *Handler) gate() (fault.Mode, error) {
	mode, delay := h.s.Fault()
	switch mode {
	case fault.Slow:
		time.Sleep(delay)
	case fault.Timeout:
		time.Sleep(delay)
		return mode, modbus.ErrServerDeviceFailure
	case fault.Error:
		return mode, modbus.ErrServerDeviceFailure
	}
	return mode, nil
}

// HandleInputRegisters serves FC4 (Thermocouple Channels 0..3).
func (h *Handler) HandleInputRegisters(req *modbus.InputRegistersRequest) ([]uint16, error) {
	mode, err := h.gate()
	if err != nil {
		return nil, err
	}
	vals, ok := h.s.InputRegisters(req.Addr, req.Quantity)
	if !ok {
		return nil, modbus.ErrIllegalDataAddress
	}
	if mode == fault.Garbage {
		for i := range vals {
			vals[i] = GarbageValue
		}
	}
	return vals, nil
}

func (h *Handler) HandleCoils(*modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *Handler) HandleHoldingRegisters(*modbus.HoldingRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *Handler) HandleDiscreteInputs(*modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}
