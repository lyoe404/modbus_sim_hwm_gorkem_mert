package temphumidity

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"time"
)

// StartMalformedListener replaces the normal sensor listener only while its
// malformed fault is active. The Modbus server library validates handler output
// before encoding, so a register handler cannot generate this fault.
func StartMalformedListener(addr string) (stop func(), err error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
			writeTruncatedReadResponse(conn)
			stopClose()
			_ = conn.Close()
		}
	}()
	return func() {
		cancel()
		_ = listener.Close()
		<-done
	}, nil
}

func writeTruncatedReadResponse(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	var request [12]byte
	if _, err := io.ReadFull(conn, request[:]); err != nil {
		return
	}
	if binary.BigEndian.Uint16(request[4:6]) != 6 || request[7] != 4 {
		return
	}
	// Preserve the transaction and unit IDs. The PDU advertises four data
	// bytes while supplying zero. The MBAP length describes what is on the wire.
	var response [9]byte
	copy(response[:8], request[:8])
	binary.BigEndian.PutUint16(response[4:6], 3)
	response[8] = 4
	_, _ = conn.Write(response[:])
}
