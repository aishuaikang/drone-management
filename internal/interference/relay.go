package interference

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"dr600ab-net/internal/model"
)

const (
	defaultRelayHost    = "192.168.100.107"
	defaultRelayPort    = 1030
	defaultRelayAddress = 1
	defaultRelayTimeout = time.Second
	relayChannelCount   = 8
	relayMaxResponse    = 1024
)

// RelayOptions configures the Zhiqian/ZQWL network relay controller.
type RelayOptions struct {
	Host    string
	Port    int
	Address int
	Timeout time.Duration
}

// RelayController sends ASCII control commands to an 8-channel network relay.
type RelayController struct {
	networkAddress string
	host           string
	port           int
	deviceAddress  int
	timeout        time.Duration
	mu             sync.Mutex
	statusMu       sync.RWMutex
	connected      bool
	connectError   string
	updatedAt      *time.Time
}

// NewRelayController creates a network relay controller.
func NewRelayController(options RelayOptions) *RelayController {
	options = normalizeRelayOptions(options)
	return &RelayController{
		networkAddress: net.JoinHostPort(options.Host, strconv.Itoa(options.Port)),
		host:           options.Host,
		port:           options.Port,
		deviceAddress:  options.Address,
		timeout:        options.Timeout,
	}
}

// Status returns the most recent relay TCP client connection state.
func (c *RelayController) Status() model.TCPClientStatus {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()

	var updatedAt *time.Time
	if c.updatedAt != nil {
		value := *c.updatedAt
		updatedAt = &value
	}
	return model.TCPClientStatus{
		Address:      c.networkAddress,
		Host:         c.host,
		Port:         c.port,
		Connected:    c.connected,
		ConnectError: c.connectError,
		UpdatedAt:    updatedAt,
	}
}

// NewRelayOutputFactory returns an output factory backed by one shared relay connection target.
func NewRelayOutputFactory(options RelayOptions) OutputFactory {
	controller := NewRelayController(options)
	return controller.Output
}

// Output creates a relay-backed Output for one relay output number.
func (c *RelayController) Output(number int) Output {
	return &RelayOutput{
		Number:     number,
		controller: c,
	}
}

// RelayOutput implements Output using one relay DO output.
type RelayOutput struct {
	Number     int
	controller *RelayController
}

// Setup validates the relay output number.
func (o *RelayOutput) Setup() error {
	return validateRelayNumber(o.Number)
}

// SetHigh closes the relay's normally-open contact.
func (o *RelayOutput) SetHigh() error {
	return o.setValue(1)
}

// SetHighFor closes the relay and lets the relay auto-open it after duration.
func (o *RelayOutput) SetHighFor(duration time.Duration) error {
	if duration <= 0 {
		return o.SetHigh()
	}
	ms := duration.Milliseconds()
	if ms > 2147483647 {
		return fmt.Errorf("relay delay exceeds 2147483647 milliseconds: %d", ms)
	}
	return o.setValueWithDelay(1, ms)
}

// SetLow opens the relay's normally-open contact.
func (o *RelayOutput) SetLow() error {
	return o.setValue(0)
}

// GetValue reads the current relay output state.
func (o *RelayOutput) GetValue() (int, error) {
	if o.controller == nil {
		return 0, fmt.Errorf("relay controller is not configured")
	}
	if err := validateRelayNumber(o.Number); err != nil {
		return 0, err
	}
	response, err := o.controller.sendASCII(fmt.Sprintf(
		"zq %d get y%02d qz",
		o.controller.deviceAddress,
		o.Number,
	))
	if err != nil {
		return 0, err
	}
	state, err := parseRelayStateResponse(response, o.controller.deviceAddress, o.Number)
	if err != nil {
		o.controller.recordStatus(err)
		return 0, err
	}
	return state.Value, nil
}

// GetState reads the current relay output state and remaining relay-side delay.
func (o *RelayOutput) GetState() (OutputState, error) {
	if o.controller == nil {
		return OutputState{}, fmt.Errorf("relay controller is not configured")
	}
	if err := validateRelayNumber(o.Number); err != nil {
		return OutputState{}, err
	}
	response, err := o.controller.sendASCII(fmt.Sprintf(
		"zq %d get y%02d qz",
		o.controller.deviceAddress,
		o.Number,
	))
	if err != nil {
		return OutputState{}, err
	}
	state, err := parseRelayStateResponse(response, o.controller.deviceAddress, o.Number)
	if err != nil {
		o.controller.recordStatus(err)
		return OutputState{}, err
	}
	return state, nil
}

// Cleanup releases local resources. Relay TCP outputs are stateless per command.
func (o *RelayOutput) Cleanup() {
}

func (o *RelayOutput) setValue(value int) error {
	return o.setValueWithDelay(value, 0)
}

func (o *RelayOutput) setValueWithDelay(value int, delayMilliseconds int64) error {
	if o.controller == nil {
		return fmt.Errorf("relay controller is not configured")
	}
	if err := validateRelayNumber(o.Number); err != nil {
		return err
	}
	command := fmt.Sprintf(
		"zq %d set y%02d %d qz",
		o.controller.deviceAddress,
		o.Number,
		value,
	)
	if delayMilliseconds > 0 {
		command = fmt.Sprintf(
			"zq %d set y%02d %d %d qz",
			o.controller.deviceAddress,
			o.Number,
			value,
			delayMilliseconds,
		)
	}
	response, err := o.controller.sendASCII(command)
	if err != nil {
		return err
	}
	state, err := parseRelayStateResponse(response, o.controller.deviceAddress, o.Number)
	if err != nil {
		o.controller.recordStatus(err)
		return err
	}
	if state.Value != value {
		err := fmt.Errorf("relay y%02d state = %d, want %d", o.Number, state.Value, value)
		o.controller.recordStatus(err)
		return err
	}
	return nil
}

func (c *RelayController) sendASCII(command string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := net.DialTimeout("tcp", c.networkAddress, c.timeout)
	if err != nil {
		err = fmt.Errorf("connect relay %s: %w", c.networkAddress, err)
		c.recordStatus(err)
		return "", err
	}
	defer conn.Close()

	deadline := time.Now().Add(c.timeout)
	if err := conn.SetDeadline(deadline); err != nil {
		err = fmt.Errorf("set relay deadline: %w", err)
		c.recordStatus(err)
		return "", err
	}
	if _, err := conn.Write([]byte(strings.TrimSpace(command))); err != nil {
		err = fmt.Errorf("write relay command: %w", err)
		c.recordStatus(err)
		return "", err
	}

	response := make([]byte, 0, 128)
	buf := make([]byte, 128)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			response = append(response, buf[:n]...)
			if relayResponseComplete(response) {
				c.recordStatus(nil)
				return strings.TrimSpace(string(response)), nil
			}
			if len(response) > relayMaxResponse {
				err := fmt.Errorf("relay response exceeds %d bytes", relayMaxResponse)
				c.recordStatus(err)
				return "", err
			}
		}
		if err != nil {
			err = fmt.Errorf("read relay response: %w", err)
			c.recordStatus(err)
			return "", err
		}
	}
}

func (c *RelayController) recordStatus(err error) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()

	now := time.Now()
	c.updatedAt = &now
	if err != nil {
		c.connected = false
		c.connectError = err.Error()
		return
	}
	c.connected = true
	c.connectError = ""
}

func relayResponseComplete(response []byte) bool {
	fields := strings.Fields(string(response))
	return len(fields) > 0 && fields[len(fields)-1] == "qz"
}

func parseRelayStateResponse(response string, address int, number int) (OutputState, error) {
	fields := strings.Fields(strings.TrimSpace(response))
	if len(fields) < 6 {
		return OutputState{}, fmt.Errorf("invalid relay response %q", response)
	}
	if fields[0] != "zq" || fields[2] != "ret" || fields[len(fields)-1] != "qz" {
		return OutputState{}, fmt.Errorf("invalid relay response %q", response)
	}
	gotAddress, err := strconv.Atoi(fields[1])
	if err != nil || gotAddress != address {
		return OutputState{}, fmt.Errorf("relay response address = %q, want %d", fields[1], address)
	}
	channelNumber, ok := parseRelayChannel(fields[3])
	if !ok || channelNumber != number {
		return OutputState{}, fmt.Errorf("relay response channel = %q, want y%02d", fields[3], number)
	}
	value, err := strconv.Atoi(fields[4])
	if err != nil || (value != 0 && value != 1) {
		return OutputState{}, fmt.Errorf("relay response state = %q", fields[4])
	}
	var remaining time.Duration
	if len(fields) >= 7 {
		delayMS, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil || delayMS < 0 {
			return OutputState{}, fmt.Errorf("relay response delay = %q", fields[5])
		}
		remaining = time.Duration(delayMS) * time.Millisecond
	}
	return OutputState{
		Value:     value,
		Remaining: remaining,
	}, nil
}

func parseRelayChannel(value string) (int, bool) {
	value = strings.TrimSpace(strings.ToLower(value))
	if !strings.HasPrefix(value, "y") {
		return 0, false
	}
	number, err := strconv.Atoi(strings.TrimLeft(value[1:], "0"))
	if err != nil {
		return 0, false
	}
	return number, true
}

func validateRelayNumber(number int) error {
	if number < 1 || number > relayChannelCount {
		return fmt.Errorf("relay output number must be between 1 and %d: %d", relayChannelCount, number)
	}
	return nil
}

func normalizeRelayOptions(options RelayOptions) RelayOptions {
	options.Host = strings.TrimSpace(options.Host)
	if options.Host == "" {
		options.Host = defaultRelayHost
	}
	if options.Port <= 0 || options.Port > 65535 {
		options.Port = defaultRelayPort
	}
	if options.Address < 0 || options.Address > 255 {
		options.Address = defaultRelayAddress
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultRelayTimeout
	}
	return options
}
