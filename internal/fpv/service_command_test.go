package fpv

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"dr600ab-net/internal/store"
)

func TestServiceSetVideoFrequencySendsATCommandAndWaitsOK(t *testing.T) {
	service, conn, cancel := startCommandTestService(t)
	defer cancel()
	defer conn.Close()

	commands := respondToCommands(t, conn, []string{"OK\r\n"})

	ctx, cancelCommand := context.WithTimeout(context.Background(), time.Second)
	defer cancelCommand()
	if err := service.SetVideoFrequency(ctx, 1360); err != nil {
		t.Fatalf("SetVideoFrequency() error = %v", err)
	}
	if got := <-commands; got != "AT+F=1360\r\n" {
		t.Fatalf("command = %q, want %q", got, "AT+F=1360\r\n")
	}
}

func TestServiceStopVideoRetriesUntilOK(t *testing.T) {
	service, conn, cancel := startCommandTestService(t)
	defer cancel()
	defer conn.Close()

	commands := respondToCommands(t, conn, []string{"ERROR\r\n", "ERROR\r\n", "OK\r\n"})

	ctx, cancelCommand := context.WithTimeout(context.Background(), time.Second)
	defer cancelCommand()
	if err := service.StopVideo(ctx); err != nil {
		t.Fatalf("StopVideo() error = %v", err)
	}
	for i := 0; i < 3; i++ {
		if got := <-commands; got != "AT+F=0\r\n" {
			t.Fatalf("command %d = %q, want %q", i+1, got, "AT+F=0\r\n")
		}
	}
}

func startCommandTestService(t *testing.T) (*Service, net.Conn, context.CancelFunc) {
	t.Helper()
	port := freeTCPPort(t)
	state := store.New(10, 10)
	service := NewService(state, Options{
		Host:           "127.0.0.1",
		Port:           port,
		CommandTimeout: time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go service.Run(ctx)
	waitFor(t, time.Second, func() bool { return service.Status().Listening })

	conn, err := net.Dial("tcp", service.Address())
	if err != nil {
		cancel()
		t.Fatalf("Dial() error = %v", err)
	}
	waitFor(t, time.Second, func() bool { return service.Status().SourceConnected })
	return service, conn, cancel
}

func respondToCommands(t *testing.T, conn net.Conn, responses []string) <-chan string {
	t.Helper()
	commands := make(chan string, len(responses))
	go func() {
		reader := bufio.NewReader(conn)
		for _, response := range responses {
			command, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			commands <- command
			if _, err := conn.Write([]byte(response)); err != nil {
				return
			}
		}
	}()
	return commands
}

func TestServiceCommandResponseDoesNotCreateFPVTarget(t *testing.T) {
	state := store.New(10, 10)
	service := NewService(state, Options{Host: "127.0.0.1", Port: 10005})

	remainder := service.ingestBuffer([]byte("OK\r\n"))
	if len(remainder) != 0 {
		t.Fatalf("remainder = %q, want empty", string(remainder))
	}
	if items := state.FPV(10); len(items) != 0 {
		t.Fatalf("items = %#v, want empty", items)
	}

	remainder = service.ingestBuffer([]byte(strings.Repeat("E", 3) + "RROR\r\n"))
	if len(remainder) != 0 {
		t.Fatalf("remainder = %q, want empty", string(remainder))
	}
}
