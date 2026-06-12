package interference

import (
	"fmt"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestRelayOutputSetAndGet(t *testing.T) {
	host, port, commands := startRelayTestServer(t, map[string]string{
		"zq 1 set y02 1 qz":      "zq 1 ret y02 1 qz",
		"zq 1 set y02 1 5000 qz": "zq 1 ret y02 1 5000 qz",
		"zq 1 get y02 qz":        "zq 1 ret y02 1 5000 qz",
		"zq 1 set y02 0 qz":      "zq 1 ret y02 0 qz",
	})
	output := NewRelayController(RelayOptions{
		Host:    host,
		Port:    port,
		Address: 1,
		Timeout: time.Second,
	}).Output(2).(*RelayOutput)

	if err := output.Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if err := output.SetHigh(); err != nil {
		t.Fatalf("SetHigh() error = %v", err)
	}
	if err := output.SetHighFor(5 * time.Second); err != nil {
		t.Fatalf("SetHighFor() error = %v", err)
	}
	value, err := output.GetValue()
	if err != nil {
		t.Fatalf("GetValue() error = %v", err)
	}
	if value != 1 {
		t.Fatalf("GetValue() = %d, want 1", value)
	}
	state, err := output.GetState()
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if state.Value != 1 || state.Remaining != 5*time.Second {
		t.Fatalf("GetState() = %#v, want value 1 remaining 5s", state)
	}
	if err := output.SetLow(); err != nil {
		t.Fatalf("SetLow() error = %v", err)
	}
	assertRelayCommands(t, commands, []string{
		"zq 1 set y02 1 qz",
		"zq 1 set y02 1 5000 qz",
		"zq 1 get y02 qz",
		"zq 1 get y02 qz",
		"zq 1 set y02 0 qz",
	})
}

func TestParseRelayStateResponse(t *testing.T) {
	tests := []struct {
		name          string
		response      string
		wantValue     int
		wantRemaining time.Duration
		wantErr       bool
	}{
		{
			name:      "open",
			response:  "zq 1 ret y02 0 qz",
			wantValue: 0,
		},
		{
			name:          "closed after delay response",
			response:      "zq 1 ret y02 1 5000 qz",
			wantValue:     1,
			wantRemaining: 5 * time.Second,
		},
		{
			name:          "unpadded channel response with delay",
			response:      "zq 1 ret y2 1 5000 qz",
			wantValue:     1,
			wantRemaining: 5 * time.Second,
		},
		{
			name:     "wrong channel",
			response: "zq 1 ret y03 1 qz",
			wantErr:  true,
		},
		{
			name:     "bad state",
			response: "zq 1 ret y02 2 qz",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRelayStateResponse(tt.response, 1, 2)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseRelayStateResponse() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRelayStateResponse() error = %v", err)
			}
			if got.Value != tt.wantValue || got.Remaining != tt.wantRemaining {
				t.Fatalf(
					"parseRelayStateResponse() = %#v, want value %d remaining %s",
					got,
					tt.wantValue,
					tt.wantRemaining,
				)
			}
		})
	}
}

func TestDefaultChannels(t *testing.T) {
	channels := DefaultChannels()
	if len(channels) != 8 {
		t.Fatalf("channel count = %d, want 8", len(channels))
	}
	wantBands := [][]string{
		{"433"},
		{"915"},
		{"1.2"},
		{"1.4"},
		{"1.5"},
		{"2.4"},
		{"5.2"},
		{"5.8"},
	}
	for index, channel := range channels {
		number := index + 1
		if channel.ID != fmt.Sprintf("io%d", number) ||
			channel.Label != fmt.Sprintf("Y%d", number) ||
			channel.Output != number ||
			channel.Reserved {
			t.Fatalf("channel %d = %#v", number, channel)
		}
		if !reflect.DeepEqual(channel.Bands, wantBands[index]) {
			t.Fatalf("channel %d bands = %#v, want %#v", number, channel.Bands, wantBands[index])
		}
	}
}

func startRelayTestServer(t *testing.T, responses map[string]string) (string, int, <-chan string) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen relay test server: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	commands := make(chan string, 16)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				command, err := readRelayTestCommand(conn)
				if err != nil {
					return
				}
				commands <- command
				response, ok := responses[command]
				if !ok {
					response = fmt.Sprintf("unexpected command %q qz", command)
				}
				_, _ = conn.Write([]byte(response))
			}()
		}
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split relay test address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse relay test port: %v", err)
	}
	return host, port, commands
}

func readRelayTestCommand(conn net.Conn) (string, error) {
	response := make([]byte, 0, 128)
	buf := make([]byte, 128)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			response = append(response, buf[:n]...)
			if relayResponseComplete(response) {
				return string(response), nil
			}
		}
		if err != nil {
			return "", err
		}
	}
}

func assertRelayCommands(t *testing.T, commands <-chan string, want []string) {
	t.Helper()

	for _, expected := range want {
		select {
		case got := <-commands:
			if got != expected {
				t.Fatalf("relay command = %q, want %q", got, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for relay command %q", expected)
		}
	}
}
