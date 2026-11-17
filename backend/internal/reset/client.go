package reset

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

type Client struct {
	Addr    string
	Timeout time.Duration
}

func Valid(target string) bool {
	switch target {
	case "temp-humidity", "relay", "thermocouple", "speed-pwm":
		return true
	}
	return false
}
func (c *Client) Command(ctx context.Context, command, target string) (string, error) {
	if !Valid(target) {
		return "", fmt.Errorf("invalid target")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	if _, err = fmt.Fprintf(conn, "%s %s\n", command, target); err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024), 4096)
	if !scanner.Scan() {
		if scanner.Err() != nil {
			return "", scanner.Err()
		}
		return "", fmt.Errorf("empty reset response")
	}
	line := scanner.Text()
	if !strings.HasPrefix(line, "OK ") {
		return "", fmt.Errorf("%s", line)
	}
	return line, nil
}
func (c *Client) Status(ctx context.Context, target string) (string, error) {
	line, err := c.Command(ctx, "STATUS", target)
	if err != nil {
		return "unknown", err
	}
	for _, part := range strings.Fields(line) {
		if strings.HasPrefix(part, "status=") {
			return strings.TrimPrefix(part, "status="), nil
		}
	}
	return "unknown", fmt.Errorf("invalid reset status")
}
