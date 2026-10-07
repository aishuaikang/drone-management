package interference

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func relayTestStateFrame(address int, command byte, values ...byte) []byte {
	frame := relayReadFrame(address, command)
	copy(frame[4:12], values)
	frame[12] = 0
	for _, value := range frame[:12] {
		frame[12] += value
	}
	return frame
}

func TestRelayReadOutputsMatchesManual(t *testing.T) {
	want := []byte{0x48, 0x3a, 0x01, 0x53, 0, 0, 0, 0, 0, 0, 0, 0, 0xd6, 0x45, 0x44}
	if got := relayReadFrame(1, relayReadOutputs); !bytes.Equal(got, want) {
		t.Fatalf("output query = %x, want %x", got, want)
	}
}

func TestRelayStreamHandlesFragmentedMixedReportsAndRejectsInvalidState(t *testing.T) {
	badChecksum := relayTestStateFrame(1, relayOutputState, 1)
	badChecksum[12]++
	badTail := relayTestStateFrame(1, relayOutputState, 1)
	badTail[14] = 0
	stream := append([]byte("noise"), badChecksum...)
	for _, frame := range [][]byte{
		badTail,
		relayTestStateFrame(2, relayOutputState, 1),
		relayTestStateFrame(1, 0x61, 1),
		relayTestStateFrame(1, relayOutputState, 2),
		relayTestStateFrame(1, relayInputState, 1),
		[]byte("zq 1 ret y01 1 10000 qz"),
		relayTestStateFrame(1, relayOutputState, 1),
		relayTestStateFrame(1, relayOutputState, 0),
	} {
		stream = append(stream, frame...)
	}
	decoder := relayStreamDecoder{}
	var updates []RelayStateUpdate
	var replies []string
	// Feed individual bytes to split every header, payload, checksum and tail.
	for _, value := range stream {
		decoder.buffer = append(decoder.buffer, value)
		for {
			message, ok := decoder.next()
			if !ok {
				break
			}
			if message.ascii != "" {
				replies = append(replies, message.ascii)
			}
			if update, ok := parseRelayStatusFrame(message.binary, 1); ok {
				updates = append(updates, update)
			}
		}
	}
	if len(updates) != 3 || !updates[0].Inputs || updates[1].Inputs ||
		updates[1].Values[0] != 1 || updates[2].Values[0] != 0 {
		t.Fatalf("valid reports = %#v", updates)
	}
	if !reflect.DeepEqual(replies, []string{"zq 1 ret y01 1 10000 qz"}) {
		t.Fatalf("ASCII replies = %#v", replies)
	}
}

func TestRelayASCIICommandIgnoresAutomaticBinaryReports(t *testing.T) {
	response := append(relayTestStateFrame(1, relayInputState, 1), relayTestStateFrame(1, relayOutputState, 1)...)
	response = append(response, []byte("zq 1 ret y01 1 10000 qz")...)
	host, port, _ := startRelayTestServer(t, map[string]string{
		"zq 1 set y01 1 10000 qz": string(response),
	})
	controller := NewRelayController(RelayOptions{Host: host, Port: port, Address: 1})
	if err := controller.Output(1).(*RelayOutput).SetHighFor(10 * time.Second); err != nil {
		t.Fatalf("timed control with automatic reports: %v", err)
	}
}

func TestRelayMonitorReceivesFastTransitionsAndReconnects(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverErrors := make(chan error, 1)
	go func() {
		for attempt := 0; attempt < 2; attempt++ {
			conn, err := listener.Accept()
			if err != nil {
				serverErrors <- err
				return
			}
			query := make([]byte, relayFrameSize)
			_, err = io.ReadFull(conn, query)
			if err != nil || !bytes.Equal(query, relayReadFrame(1, relayReadOutputs)) {
				_ = conn.Close()
				serverErrors <- fmt.Errorf("relay query = %x: %v", query, err)
				return
			}
			if attempt == 0 {
				frames := append(relayTestStateFrame(1, relayOutputState, 0), relayTestStateFrame(1, relayInputState, 1)...)
				frames = append(frames, relayTestStateFrame(1, relayOutputState, 1)...)
				frames = append(frames, relayTestStateFrame(1, relayOutputState, 1)...)
				frames = append(frames, relayTestStateFrame(1, relayOutputState, 0)...)
				// Transitions happen without waiting for the one-second poll.
				_, _ = conn.Write(frames[:7])
				_, _ = conn.Write(frames[7:])
				_ = conn.Close()
			} else {
				_, _ = conn.Write(relayTestStateFrame(1, relayOutputState, 0, 1))
				_, _ = io.Copy(io.Discard, conn)
				_ = conn.Close()
			}
		}
		serverErrors <- nil
	}()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	controller := NewRelayController(RelayOptions{Host: host, Port: port, Address: 1})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	updates := make(chan RelayStateUpdate, 32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		controller.Monitor(ctx, func(update RelayStateUpdate) { updates <- update })
	}()
	var outputValues []int
	sawInput, sawDisconnect, sawReconnect := false, false, false
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	for !sawReconnect {
		select {
		case update := <-updates:
			if update.Error != nil {
				sawDisconnect = true
			} else if update.Inputs {
				sawInput = true
			} else if update.Values[1] == 1 {
				sawReconnect = true
			} else {
				outputValues = append(outputValues, update.Values[0])
			}
		case <-timer.C:
			t.Fatalf("monitor timeout: outputs=%v input=%v disconnect=%v", outputValues, sawInput, sawDisconnect)
		}
	}
	if !reflect.DeepEqual(outputValues, []int{0, 1, 1, 0}) || !sawInput || !sawDisconnect {
		t.Fatalf("observations: outputs=%v input=%v disconnect=%v", outputValues, sawInput, sawDisconnect)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop after cancellation")
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestRelayMonitorDiscardsQuerySpanningSoftwareControl(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	queryReceived := make(chan struct{})
	releaseOldReply := make(chan struct{})
	serverErrors := make(chan error, 1)
	go func() {
		monitor, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer monitor.Close()
		_ = monitor.SetDeadline(time.Now().Add(4 * time.Second))
		query := make([]byte, relayFrameSize)
		if _, err := io.ReadFull(monitor, query); err != nil {
			serverErrors <- err
			return
		}
		close(queryReceived)
		control, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		_ = control.SetDeadline(time.Now().Add(time.Second))
		command, err := readRelayTestCommand(control)
		if err == nil && command == "zq 1 set y01 1 qz" {
			_, err = control.Write([]byte("zq 1 ret y01 1 qz"))
		}
		_ = control.Close()
		if err != nil || command != "zq 1 set y01 1 qz" {
			serverErrors <- fmt.Errorf("control = %q: %v", command, err)
			return
		}
		<-releaseOldReply
		oldReplies := append(relayTestStateFrame(1, relayOutputState, 0), relayTestStateFrame(1, relayOutputState, 0)...)
		_, _ = monitor.Write(oldReplies)
		_ = monitor.Close()
		monitor, err = listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer monitor.Close()
		_ = monitor.SetDeadline(time.Now().Add(4 * time.Second))
		if _, err := io.ReadFull(monitor, query); err != nil {
			serverErrors <- err
			return
		}
		_, _ = monitor.Write(relayTestStateFrame(1, relayOutputState, 1))
		_, _ = io.Copy(io.Discard, monitor)
		serverErrors <- nil
	}()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	controller := NewRelayController(RelayOptions{Host: host, Port: port, Address: 1, Timeout: 2 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	updates := make(chan RelayStateUpdate, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		controller.Monitor(ctx, func(update RelayStateUpdate) { updates <- update })
	}()
	select {
	case <-queryReceived:
	case <-time.After(time.Second):
		t.Fatal("monitor did not query initial output state")
	}
	if err := controller.Output(1).SetHigh(); err != nil {
		t.Fatal(err)
	}
	close(releaseOldReply)
	select {
	case update := <-updates:
		if update.Error != nil || update.Inputs || update.Values[0] != 1 {
			t.Fatalf("monitor applied old query after successful control: %#v", update)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not retry superseded output query")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop")
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}
