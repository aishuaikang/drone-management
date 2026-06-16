package fpv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"drone-management/internal/model"
	"drone-management/internal/store"
)

const defaultBufferLimit = 1024 * 1024
const defaultCommandTimeout = 3 * time.Second

var errListenerConfigurationChanged = errors.New("listener configuration changed")

var (
	// ErrNoCommandConnection means no FPV source TCP client is available for AT commands.
	ErrNoCommandConnection = errors.New("fpv source is not connected")
	// ErrInvalidCommandFrequency means the requested FPV video frequency is invalid.
	ErrInvalidCommandFrequency = errors.New("invalid fpv video frequency")
)

// ListenerOpener opens a TCP listener. Tests can replace it.
type ListenerOpener func(network string, address string) (net.Listener, error)

// Options configures the A3-F9 FPV alarm receiver.
type Options struct {
	Host              string
	Port              int
	BindRetryInterval time.Duration
	ReadIdleTimeout   time.Duration
	CommandTimeout    time.Duration
	BufferLimit       int
	OpenListener      ListenerOpener
}

// Service receives A3-F9 FPV alarm messages over TCP.
type Service struct {
	store   *store.Store
	options Options

	mu              sync.RWMutex
	commandMu       sync.Mutex
	listener        net.Listener
	listening       bool
	listenError     string
	sourceConnected bool
	clientAddress   string
	updatedAt       time.Time
	clients         map[string]net.Conn
	commandWaiters  []chan string
	configVersion   uint64
}

// NewService creates an FPV receiver.
func NewService(store *store.Store, options Options) *Service {
	return &Service{
		store:   store,
		options: normalizeOptions(options),
	}
}

// Run keeps the TCP server alive until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		listener, err := s.openListener()
		if err != nil {
			if errors.Is(err, errListenerConfigurationChanged) {
				continue
			}
			s.setListenerState(false, err.Error())
			if !sleepOrDone(ctx, s.bindRetryInterval()) {
				return
			}
			continue
		}

		s.setListenerState(true, "")
		err = s.serveListener(ctx, listener)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.setListenerState(false, err.Error())
		}
		if err == nil {
			continue
		}
		if !sleepOrDone(ctx, s.bindRetryInterval()) {
			return
		}
	}
}

// Address returns the listener address.
func (s *Service) Address() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addressLocked()
}

// Status returns a snapshot of the receiver state.
func (s *Service) Status() model.TCPListenerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusLocked()
}

// SetPort updates the listener port and restarts the TCP listener.
func (s *Service) SetPort(port int) {
	s.mu.Lock()
	if s.options.Port == port {
		s.mu.Unlock()
		return
	}
	s.options.Port = port
	s.configVersion++
	listener := s.listener
	s.listener = nil
	clients := make([]net.Conn, 0, len(s.clients))
	for _, conn := range s.clients {
		clients = append(clients, conn)
	}
	s.clients = map[string]net.Conn{}
	s.clientAddress = ""
	s.sourceConnected = false
	s.commandWaiters = nil
	s.listening = false
	s.listenError = ""
	s.updatedAt = time.Now()
	s.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	for _, conn := range clients {
		_ = conn.Close()
	}
	if len(clients) > 0 {
		slog.Info("FPV TCP 端口已更新，旧连接已断开", "clients", len(clients), "port", port)
	}
}

// SetVideoFrequency switches the FPV video receiver to a frequency and waits for OK.
func (s *Service) SetVideoFrequency(ctx context.Context, frequency int) error {
	if frequency <= 0 {
		return ErrInvalidCommandFrequency
	}
	return s.sendATCommand(ctx, fmt.Sprintf("AT+F=%d\r\n", frequency))
}

// StopVideo switches FPV video reception off, retrying until OK or the retry limit is reached.
func (s *Service) StopVideo(ctx context.Context) error {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := s.sendATCommand(ctx, "AT+F=0\r\n")
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil || attempt == maxAttempts {
			break
		}
		if !sleepOrDone(ctx, 100*time.Millisecond) {
			break
		}
	}
	return fmt.Errorf("stop fpv video: %w", lastErr)
}

func (s *Service) serveListener(ctx context.Context, listener net.Listener) error {
	listenerDone := make(chan struct{})
	defer func() {
		close(listenerDone)
		_ = listener.Close()
		s.clearListener(listener)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-listenerDone:
		}
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept A3-F9 FPV connection: %w", err)
		}
		go s.handleConnection(ctx, conn)
	}
}

func (s *Service) handleConnection(ctx context.Context, conn net.Conn) {
	clientAddress := conn.RemoteAddr().String()
	s.setSourceConnection(true, clientAddress, conn)
	defer func() {
		_ = conn.Close()
		s.setSourceConnection(false, clientAddress, nil)
	}()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	var buffer []byte
	chunk := make([]byte, 4096)
	for {
		if timeout := s.readIdleTimeout(); timeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(timeout))
		}
		n, err := conn.Read(chunk)
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)
			buffer = s.ingestBuffer(buffer)
			if len(buffer) > s.options.BufferLimit {
				buffer = buffer[len(buffer)-s.options.BufferLimit:]
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				slog.Warn("读取 A3-F9 FPV TCP 数据失败", "client", clientAddress, "error", err)
			}
			return
		}
	}
}

func (s *Service) ingestBuffer(buffer []byte) []byte {
	for {
		buffer = trimLeadingSeparators(buffer)
		if len(buffer) == 0 {
			return buffer
		}

		switch buffer[0] {
		case 'F', '$':
			lineEnd := bytes.IndexAny(buffer, "\r\n")
			if lineEnd == -1 {
				return buffer
			}
			line := string(buffer[:lineEnd])
			buffer = consumeLineEnd(buffer[lineEnd:])
			s.ingestASCII(line)
		case 'O', 'E':
			lineEnd := bytes.IndexAny(buffer, "\r\n")
			if lineEnd == -1 {
				return buffer
			}
			line := string(buffer[:lineEnd])
			buffer = consumeLineEnd(buffer[lineEnd:])
			s.ingestCommandResponse(line)
		case 0xfe:
			if len(buffer) < 8 {
				return buffer
			}
			s.ingestFormat4(buffer[:8])
			buffer = buffer[8:]
		case 0x1f:
			if len(buffer) < 16 {
				return buffer
			}
			s.ingestFormat5(buffer[:16])
			buffer = buffer[16:]
		default:
			next := nextFrameStart(buffer[1:])
			if next == -1 {
				return nil
			}
			buffer = buffer[next+1:]
		}
	}
}

func (s *Service) ingestASCII(line string) {
	target, err := ParseASCII(line, time.Now())
	if err != nil {
		slog.Debug("忽略无效 A3-F9 ASCII 告警", "line", line, "error", err)
		return
	}
	s.store.AddFPV(target)
}

func (s *Service) ingestCommandResponse(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	upper := strings.ToUpper(line)
	if upper != "OK" && !strings.Contains(upper, "ERROR") {
		return
	}
	s.notifyCommandResponse(line)
}

func (s *Service) ingestFormat4(frame []byte) {
	target, err := ParseFormat4(frame, time.Now())
	if err != nil {
		slog.Debug("忽略无效 A3-F9 HEX8 告警", "error", err)
		return
	}
	s.store.AddFPV(target)
}

func (s *Service) ingestFormat5(frame []byte) {
	target, err := ParseFormat5(frame, time.Now())
	if err != nil {
		slog.Debug("忽略无效 A3-F9 HEX16 告警", "error", err)
		return
	}
	s.store.AddFPV(target)
}

func (s *Service) setListenerState(listening bool, listenError string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listening = listening
	s.listenError = listenError
	s.updatedAt = time.Now()
}

func (s *Service) setSourceConnection(connected bool, clientAddress string, conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients == nil {
		s.clients = make(map[string]net.Conn)
	}
	s.sourceConnected = connected
	if connected {
		s.clients[clientAddress] = conn
		s.clientAddress = clientAddress
	} else if s.clientAddress == clientAddress {
		delete(s.clients, clientAddress)
		s.clientAddress = ""
		for address := range s.clients {
			s.clientAddress = address
			break
		}
	} else {
		delete(s.clients, clientAddress)
	}
	s.sourceConnected = len(s.clients) > 0
	s.updatedAt = time.Now()
}

func (s *Service) sendATCommand(ctx context.Context, command string) error {
	s.commandMu.Lock()
	defer s.commandMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, s.commandTimeout())
	defer cancel()

	conn, address, ok := s.activeCommandConnection()
	if !ok {
		return ErrNoCommandConnection
	}

	responseCh := make(chan string, 4)
	s.registerCommandWaiter(responseCh)
	defer s.unregisterCommandWaiter(responseCh)

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
		defer conn.SetWriteDeadline(time.Time{})
	}
	if _, err := io.WriteString(conn, command); err != nil {
		return fmt.Errorf("send fpv command to %s: %w", address, err)
	}

	trimmedCommand := strings.TrimSpace(command)
	for {
		select {
		case response := <-responseCh:
			normalized := strings.ToUpper(strings.TrimSpace(response))
			switch {
			case normalized == "OK":
				return nil
			case strings.Contains(normalized, "ERROR"):
				return fmt.Errorf("fpv command %q rejected: %s", trimmedCommand, response)
			}
		case <-ctx.Done():
			return fmt.Errorf("wait fpv command %q response: %w", trimmedCommand, ctx.Err())
		}
	}
}

func (s *Service) activeCommandConnection() (net.Conn, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.clientAddress != "" {
		if conn := s.clients[s.clientAddress]; conn != nil {
			return conn, s.clientAddress, true
		}
	}
	for address, conn := range s.clients {
		if conn != nil {
			return conn, address, true
		}
	}
	return nil, "", false
}

func (s *Service) registerCommandWaiter(waiter chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commandWaiters = append(s.commandWaiters, waiter)
}

func (s *Service) unregisterCommandWaiter(waiter chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, current := range s.commandWaiters {
		if current == waiter {
			s.commandWaiters = append(s.commandWaiters[:index], s.commandWaiters[index+1:]...)
			return
		}
	}
}

func (s *Service) notifyCommandResponse(response string) {
	s.mu.RLock()
	waiters := append([]chan string(nil), s.commandWaiters...)
	s.mu.RUnlock()
	for _, waiter := range waiters {
		select {
		case waiter <- response:
		default:
		}
	}
}

func (s *Service) statusLocked() model.TCPListenerStatus {
	var updatedAt *time.Time
	if !s.updatedAt.IsZero() {
		value := s.updatedAt
		updatedAt = &value
	}
	return model.TCPListenerStatus{
		Address:         s.addressLocked(),
		Host:            s.options.Host,
		Port:            s.options.Port,
		Listening:       s.listening,
		ListenError:     s.listenError,
		SourceConnected: s.sourceConnected,
		ClientAddress:   s.clientAddress,
		UpdatedAt:       updatedAt,
	}
}

func (s *Service) openListener() (net.Listener, error) {
	s.mu.RLock()
	address := s.addressLocked()
	openListener := s.options.OpenListener
	configVersion := s.configVersion
	s.mu.RUnlock()
	listener, err := openListener("tcp", address)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.configVersion != configVersion {
		s.mu.Unlock()
		_ = listener.Close()
		return nil, errListenerConfigurationChanged
	}
	s.listener = listener
	s.mu.Unlock()
	return listener, nil
}

func (s *Service) clearListener(listener net.Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == listener {
		s.listener = nil
	}
	if s.listening {
		s.listening = false
		s.updatedAt = time.Now()
	}
}

func (s *Service) addressLocked() string {
	return net.JoinHostPort(s.options.Host, strconv.Itoa(s.options.Port))
}

func (s *Service) bindRetryInterval() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.options.BindRetryInterval
}

func (s *Service) readIdleTimeout() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.options.ReadIdleTimeout
}

func (s *Service) commandTimeout() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.options.CommandTimeout
}

func normalizeOptions(options Options) Options {
	options.Host = strings.TrimSpace(options.Host)
	if options.Host == "" {
		options.Host = "0.0.0.0"
	}
	if options.Port == 0 {
		options.Port = 10005
	}
	if options.BindRetryInterval <= 0 {
		options.BindRetryInterval = time.Second
	}
	if options.CommandTimeout <= 0 {
		options.CommandTimeout = defaultCommandTimeout
	}
	if options.BufferLimit <= 0 {
		options.BufferLimit = defaultBufferLimit
	}
	if options.OpenListener == nil {
		options.OpenListener = net.Listen
	}
	return options
}

func sleepOrDone(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func trimLeadingSeparators(buffer []byte) []byte {
	for len(buffer) > 0 && (buffer[0] == '\r' || buffer[0] == '\n' || buffer[0] == ' ' || buffer[0] == '\t') {
		buffer = buffer[1:]
	}
	return buffer
}

func consumeLineEnd(buffer []byte) []byte {
	for len(buffer) > 0 && (buffer[0] == '\r' || buffer[0] == '\n') {
		buffer = buffer[1:]
	}
	return buffer
}

func nextFrameStart(buffer []byte) int {
	for index, value := range buffer {
		if value == 'F' || value == '$' || value == 0xfe || value == 0x1f {
			return index
		}
	}
	return -1
}
