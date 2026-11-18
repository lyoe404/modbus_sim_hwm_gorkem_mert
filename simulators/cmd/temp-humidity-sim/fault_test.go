package main

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/simonvetter/modbus"

	"sim/internal/fault"
	"sim/internal/temphumidity"
)

func TestMalformedFaultWritesTruncatedPDUAndRecovers(t *testing.T) {
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := reservation.Addr().String()
	_ = reservation.Close()
	store := temphumidity.NewStore()
	sup := &supervisor{addr: addr, store: store, h: temphumidity.NewHandler(store)}
	defer sup.down()
	set := func(mode fault.Mode) {
		t.Helper()
		if err := sup.setFault(mode, 0); err != nil {
			t.Fatal(err)
		}
	}
	read := func() ([]uint16, error) {
		client, err := modbus.NewClient(&modbus.ClientConfiguration{URL: "tcp://" + addr, Timeout: time.Second})
		if err != nil {
			return nil, err
		}
		if err := client.Open(); err != nil {
			return nil, err
		}
		defer client.Close()
		return client.ReadRegisters(0, 2, modbus.INPUT_REGISTER)
	}
	set(fault.Garbage)
	if regs, err := read(); err != nil || len(regs) != 2 || regs[0] != temphumidity.GarbageValue {
		t.Fatalf("garbage should use correctly framed registers: %v, %v", regs, err)
	}
	set(fault.Malformed)
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write([]byte{0, 17, 0, 0, 0, 6, 1, 4, 0, 0, 0, 2}); err != nil {
		t.Fatal(err)
	}
	frame, err := io.ReadAll(conn)
	if err != nil || len(frame) != 9 || frame[1] != 17 ||
		binary.BigEndian.Uint16(frame[4:6]) != 3 || frame[7] != 4 || frame[8] != 4 {
		t.Fatalf("unexpected truncated FC4 frame %x: %v", frame, err)
	}
	if _, err := read(); !errors.Is(err, modbus.ErrProtocolError) {
		t.Fatalf("wanted protocol error, got %v", err)
	}
	set(fault.None)
	if regs, err := read(); err != nil || len(regs) != 2 || regs[0] == temphumidity.GarbageValue {
		t.Fatalf("normal reads did not resume: %v, %v", regs, err)
	}
}
