package relay

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

// HandleCoils serves FC1 (read), FC5 (single write), FC15 (multiple write).
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
	if mode == fault.Garbage {
		for i := range vals {
			vals[i] = !vals[i] // read-back mismatch simulation
		}
	}
	return vals, nil
}

func (h *Handler) HandleInputRegisters(*modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *Handler) HandleHoldingRegisters(*modbus.HoldingRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

func (h *Handler) HandleDiscreteInputs(*modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}
