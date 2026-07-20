package fpv

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"drone-management/internal/store"
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

func TestServiceRunWaitsForConnectionHandlersBeforeReturning(t *testing.T) {
	state := store.New(10, 10)
	payload := []byte(withASCIIChecksum("F5750R098T=FPV#") + "\r\n")
	conn := newShutdownBlockingConn(payload)
	listener := newSingleConnListener(conn)
	service := NewService(state, Options{
		Host: "127.0.0.1",
		Port: 10005,
		OpenListener: func(_, _ string) (net.Listener, error) {
			return listener, nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		service.Run(ctx)
		close(runDone)
	}()

	select {
	case <-conn.readStarted:
	case <-time.After(time.Second):
		t.Fatal("connection handler did not start reading")
	}
	cancel()
	select {
	case <-conn.closed:
	case <-time.After(time.Second):
		t.Fatal("connection was not closed after cancellation")
	}
	select {
	case <-runDone:
		t.Fatal("Service.Run returned before the connection handler finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(conn.readRelease)
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Service.Run did not return after the connection handler finished")
	}
	if items := state.FPV(10); len(items) != 1 || items[0].Frequency != 5750 {
		t.Fatalf("final connection payload was not processed before Run returned: %#v", items)
	}
}

type singleConnListener struct {
	mu        sync.Mutex
	conn      net.Conn
	accepted  bool
	closed    chan struct{}
	closeOnce sync.Once
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	return &singleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.accepted {
		l.accepted = true
		conn := l.conn
		l.mu.Unlock()
		return conn, nil
	}
	l.mu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return testNetAddr("listener")
}

type shutdownBlockingConn struct {
	payload     []byte
	readStarted chan struct{}
	readRelease chan struct{}
	closed      chan struct{}
	startOnce   sync.Once
	closeOnce   sync.Once
}

func newShutdownBlockingConn(payload []byte) *shutdownBlockingConn {
	return &shutdownBlockingConn{
		payload:     append([]byte(nil), payload...),
		readStarted: make(chan struct{}),
		readRelease: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (c *shutdownBlockingConn) Read(buffer []byte) (int, error) {
	c.startOnce.Do(func() { close(c.readStarted) })
	<-c.readRelease
	return copy(buffer, c.payload), io.EOF
}

func (c *shutdownBlockingConn) Write(buffer []byte) (int, error) {
	return len(buffer), nil
}

func (c *shutdownBlockingConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *shutdownBlockingConn) LocalAddr() net.Addr              { return testNetAddr("local") }
func (c *shutdownBlockingConn) RemoteAddr() net.Addr             { return testNetAddr("remote") }
func (c *shutdownBlockingConn) SetDeadline(time.Time) error      { return nil }
func (c *shutdownBlockingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *shutdownBlockingConn) SetWriteDeadline(time.Time) error { return nil }

type testNetAddr string

func (a testNetAddr) Network() string { return "test" }
func (a testNetAddr) String() string  { return string(a) }
