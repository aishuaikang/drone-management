package interference

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

var errRelayMonitorResync = errors.New("relay control changed during status query")

const (
	relayFrameSize    = 15
	relayInputState   = 0x41
	relayOutputState  = 0x54
	relayReadOutputs  = 0x53
	relayPollInterval = time.Second
)

// RelayStateUpdate carries a validated DI/DO snapshot or a connection failure.
type RelayStateUpdate struct {
	Inputs bool
	Values [relayChannelCount]int
	Time   time.Time
	Error  error
}

// Monitor keeps a separate read-only connection open for unsolicited status
// reports. Output polling also recovers changes after reconnects or lost reports.
func (c *RelayController) Monitor(ctx context.Context, observe func(RelayStateUpdate)) {
	updates := make(chan RelayStateUpdate, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for update := range updates {
			observe(update)
		}
	}()
	defer func() {
		close(updates)
		<-done
	}()
	emit := func(update RelayStateUpdate) {
		select {
		case updates <- update:
		case <-ctx.Done():
		}
	}
	for ctx.Err() == nil {
		err := c.monitorConnection(ctx, emit)
		if ctx.Err() != nil {
			return
		}
		if !errors.Is(err, errRelayMonitorResync) {
			c.recordStatus(err)
			emit(RelayStateUpdate{Time: time.Now(), Error: err})
		}
		if !sleepOrDone(ctx, relayPollInterval) {
			return
		}
	}
}

func (c *RelayController) monitorConnection(ctx context.Context, observe func(RelayStateUpdate)) error {
	dialer := net.Dialer{Timeout: c.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.networkAddress)
	if err != nil {
		return fmt.Errorf("connect relay monitor %s: %w", c.networkAddress, err)
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()

	var responseDeadline time.Time
	nextPoll := time.Now()
	awaitingOutput := false
	var queryVersion uint64
	query := func() error {
		now := time.Now()
		queryVersion = c.controlVersion.Load()
		if err := conn.SetWriteDeadline(now.Add(c.timeout)); err != nil {
			return err
		}
		if _, err := conn.Write(relayReadFrame(c.deviceAddress, relayReadOutputs)); err != nil {
			return fmt.Errorf("query relay outputs: %w", err)
		}
		awaitingOutput = true
		responseDeadline = now.Add(c.timeout)
		nextPoll = now.Add(relayPollInterval)
		return nil
	}

	decoder := relayStreamDecoder{}
	buf := make([]byte, 512)
	for ctx.Err() == nil {
		now := time.Now()
		if !awaitingOutput && !now.Before(nextPoll) {
			if err := query(); err != nil {
				return err
			}
		}
		deadline := nextPoll
		if awaitingOutput {
			deadline = responseDeadline
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return err
		}
		n, readErr := conn.Read(buf)
		receivedAt := time.Now()
		if n > 0 {
			decoder.buffer = append(decoder.buffer, buf[:n]...)
			for {
				message, ok := decoder.next()
				if !ok {
					break
				}
				update, ok := parseRelayStatusFrame(message.binary, c.deviceAddress)
				if !ok {
					continue
				}
				update.Time = receivedAt
				if !update.Inputs {
					version := c.controlVersion.Load()
					if version%2 != 0 || (awaitingOutput && queryVersion != version) {
						// Replies have no request ID. Reconnect to discard all old
						// queued replies before querying the post-command state.
						return errRelayMonitorResync
					}
					awaitingOutput = false
					c.recordStatus(nil)
				}
				observe(update)
				if update.Inputs && !awaitingOutput {
					// DI is only a trigger to query actual DO state, never evidence
					// that an interference output has been switched on.
					if err := query(); err != nil {
						return err
					}
				}
			}
			if len(decoder.buffer) > relayMaxResponse {
				return fmt.Errorf("relay monitor response exceeds %d bytes", relayMaxResponse)
			}
		}
		if readErr != nil {
			if timeout, ok := readErr.(net.Error); ok && timeout.Timeout() &&
				(!awaitingOutput || time.Now().Before(responseDeadline)) {
				continue
			}
			return fmt.Errorf("read relay monitor: %w", readErr)
		}
	}
	return ctx.Err()
}

func relayReadFrame(address int, command byte) []byte {
	frame := make([]byte, relayFrameSize)
	frame[0], frame[1], frame[2], frame[3] = 0x48, 0x3a, byte(address), command
	for _, value := range frame[:12] {
		frame[12] += value
	}
	frame[13], frame[14] = 0x45, 0x44
	return frame
}

func parseRelayStatusFrame(frame []byte, address int) (RelayStateUpdate, bool) {
	if len(frame) != relayFrameSize || int(frame[2]) != address ||
		(frame[3] != relayInputState && frame[3] != relayOutputState) {
		return RelayStateUpdate{}, false
	}
	update := RelayStateUpdate{Inputs: frame[3] == relayInputState}
	for index, value := range frame[4:12] {
		if value > 1 {
			return RelayStateUpdate{}, false
		}
		update.Values[index] = int(value)
	}
	return update, true
}

type relayMessage struct {
	ascii  string
	binary []byte
}

// relayStreamDecoder accepts split/coalesced frames and mixed ASCII replies and
// binary reports. Invalid binary frames are discarded while searching for the
// next frame header; no invalid state reaches the service.
type relayStreamDecoder struct {
	buffer []byte
}

func (d *relayStreamDecoder) next() (relayMessage, bool) {
	for len(d.buffer) >= 2 {
		switch {
		case bytes.HasPrefix(d.buffer, []byte("zq")):
			end := bytes.Index(d.buffer[2:], []byte("qz"))
			if end < 0 {
				return relayMessage{}, false
			}
			end += 4
			message := relayMessage{ascii: string(d.buffer[:end])}
			d.buffer = d.buffer[end:]
			return message, true
		case d.buffer[0] == 0x48 && d.buffer[1] == 0x3a:
			if len(d.buffer) < 4 {
				return relayMessage{}, false
			}
			size := relayFrameSize
			if command := d.buffer[3]; command >= 0x70 && command <= 0x72 {
				size = 10
			}
			if len(d.buffer) < size {
				return relayMessage{}, false
			}
			valid := d.buffer[size-2] == 0x45 && d.buffer[size-1] == 0x44
			if size == relayFrameSize {
				var checksum byte
				for _, value := range d.buffer[:12] {
					checksum += value
				}
				valid = valid && checksum == d.buffer[12]
			}
			if valid {
				message := relayMessage{binary: append([]byte(nil), d.buffer[:size]...)}
				d.buffer = d.buffer[size:]
				return message, true
			}
		}
		d.buffer = d.buffer[1:]
	}
	if len(d.buffer) == 1 && d.buffer[0] != 'H' && d.buffer[0] != 'z' {
		d.buffer = nil
	}
	return relayMessage{}, false
}
