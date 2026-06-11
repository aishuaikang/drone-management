package position

import (
	"bufio"
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

	"dr600ab-net/internal/diddecrypt"
	"dr600ab-net/internal/model"
	"dr600ab-net/internal/store"
)

const defaultMaxLineBytes = 1024 * 1024

var errListenerConfigurationChanged = errors.New("listener configuration changed")

// ListenerOpener opens a TCP listener. Tests can replace it.
type ListenerOpener func(network string, address string) (net.Listener, error)

// DIDDecoder decrypts O3+/O4 DID encrypted packets.
type DIDDecoder interface {
	DecodeDID(
		ctx context.Context,
		packet diddecrypt.Packet,
		raw string,
		receivedAt time.Time,
	) (model.ScreenPositionTarget, bool)
}

// Options configures the ddsT1 receiver.
type Options struct {
	Host              string
	Port              int
	BindRetryInterval time.Duration
	ReadIdleTimeout   time.Duration
	MaxLineBytes      int
	OpenListener      ListenerOpener
	O3Decrypt         O3DecryptOptions
	DIDDecoder        DIDDecoder
}

// Service receives ddsT1 positioning messages over TCP.
type Service struct {
	store   *store.Store
	options Options
	decoder DIDDecoder

	mu              sync.RWMutex
	listener        net.Listener
	listening       bool
	listenError     string
	sourceConnected bool
	clientAddress   string
	clients         map[string]net.Conn
	updatedAt       time.Time
	configVersion   uint64
}

// NewService creates a positioning receiver.
func NewService(store *store.Store, options Options) *Service {
	options = normalizeOptions(options)
	decoder := options.DIDDecoder
	if decoder == nil {
		decoder = NewMQTTO4DIDDecoder(options.O3Decrypt)
	}
	return &Service{
		store:   store,
		options: options,
		decoder: decoder,
		clients: map[string]net.Conn{},
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
		slog.Info("定位 TCP 端口已更新，旧连接已断开", "clients", len(clients), "port", port)
	}
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
			return fmt.Errorf("accept ddsT1 connection: %w", err)
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

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), s.options.MaxLineBytes)
	for scanner.Scan() {
		if timeout := s.readIdleTimeout(); timeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(timeout))
		}
		s.IngestLine(scanner.Text())
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
		slog.Warn("读取 ddsT1 TCP 数据失败", "client", clientAddress, "error", err)
	}
}

// IngestLine parses and stores one ddsT1 line.
func (s *Service) IngestLine(raw string) {
	receivedAt := time.Now()
	parsed, ok := ParseLine(raw, receivedAt)
	if !ok {
		return
	}
	if parsed.Location != nil {
		s.store.UpdateDeviceLocation(*parsed.Location)
	}
	if parsed.Position != nil {
		target := *parsed.Position
		if !isUncrackedDIDFallback(target) ||
			!s.store.HasCrackedScreenPositionByCorrelationID(target.CorrelationID) {
			s.store.AddPosition(target)
		}
	}
	if parsed.EncryptedDID != nil {
		s.ingestEncryptedDID(*parsed.EncryptedDID, raw, receivedAt)
	}
}

// SetDIDDecoder replaces the DID decryptor, mainly for tests.
func (s *Service) SetDIDDecoder(decoder DIDDecoder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decoder = decoder
}

func (s *Service) ingestEncryptedDID(packet diddecrypt.Packet, raw string, receivedAt time.Time) {
	s.mu.RLock()
	decoder := s.decoder
	s.mu.RUnlock()
	if decoder == nil {
		return
	}

	go func() {
		target, ok := decoder.DecodeDID(context.Background(), packet, raw, receivedAt)
		if !ok {
			return
		}
		target = enrichDIDTarget(target, packet, raw, receivedAt)
		if !target.Cracked {
			return
		}
		s.store.RemoveUncrackedDIDScreenPositionByCorrelationID(target.CorrelationID)
		s.store.AddPosition(target)
	}()
}

func enrichDIDTarget(
	target model.ScreenPositionTarget,
	packet diddecrypt.Packet,
	raw string,
	receivedAt time.Time,
) model.ScreenPositionTarget {
	if target.CorrelationID == "" {
		target.CorrelationID = didCorrelationID(packet.EncryptedID)
	}
	if target.Source == "" {
		target.Source = diddecrypt.O4Source
	}
	if target.Device == "" {
		target.Device = packet.Device
	}
	if target.Frequency == 0 {
		target.Frequency = packet.Freq
	}
	if target.RSSI == 0 {
		target.RSSI = packet.RSSI
	}
	if target.FirstSeen.IsZero() {
		target.FirstSeen = receivedAt
	}
	if target.LastSeen.IsZero() {
		target.LastSeen = receivedAt
	}

	record := target.LastRecord
	if record.Type == "" {
		record.Type = diddecrypt.O4Source
	}
	if record.ReceivedAt.IsZero() {
		record.ReceivedAt = receivedAt
	}
	if record.Device == "" {
		record.Device = packet.Device
	}
	if record.Serial == "" {
		record.Serial = target.Serial
	}
	if record.Model == "" {
		record.Model = target.Model
	}
	if record.Frequency == 0 {
		record.Frequency = packet.Freq
	}
	if record.RSSI == 0 {
		record.RSSI = packet.RSSI
	}
	if record.Raw == "" {
		record.Raw = raw
	}
	record.Cracked = target.Cracked
	target.LastRecord = record
	return target
}

func isUncrackedDIDFallback(target model.ScreenPositionTarget) bool {
	return !target.Cracked &&
		strings.TrimSpace(target.CorrelationID) != "" &&
		strings.EqualFold(strings.TrimSpace(target.Source), diddecrypt.O4Source) &&
		strings.EqualFold(strings.TrimSpace(target.Model), diddecrypt.FallbackModel)
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
	if connected {
		if s.clients == nil {
			s.clients = map[string]net.Conn{}
		}
		s.clients[clientAddress] = conn
		s.clientAddress = clientAddress
	} else {
		delete(s.clients, clientAddress)
		if s.clientAddress == clientAddress {
			s.clientAddress = ""
			for address := range s.clients {
				s.clientAddress = address
				break
			}
		}
	}
	s.sourceConnected = len(s.clients) > 0
	s.updatedAt = time.Now()
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

func normalizeOptions(options Options) Options {
	options.Host = strings.TrimSpace(options.Host)
	if options.Host == "" {
		options.Host = "0.0.0.0"
	}
	if options.Port == 0 {
		options.Port = 10007
	}
	if options.BindRetryInterval <= 0 {
		options.BindRetryInterval = time.Second
	}
	if options.MaxLineBytes <= 0 {
		options.MaxLineBytes = defaultMaxLineBytes
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
