package speedpwm

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

// HandleHoldingRegisters serves FC3 (read), FC6 (single write), FC16 (multiple write).
func (h *Handler) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	mode, err := h.gate()
	if err != nil {
		return nil, err
	}
	if req.IsWrite {
		okAddr, okVal := h.s.WriteHolding(req.Addr, req.Args)
		if !okAddr {
			return nil, modbus.ErrIllegalDataAddress
		}
		if !okVal {
			return nil, modbus.ErrIllegalDataValue
		}
		return nil, nil
	}
	vals, ok := h.s.HoldingRegisters(req.Addr, req.Quantity)
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

func (h *Handler) HandleInputRegisters(*modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *Handler) HandleDiscreteInputs(*modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}
