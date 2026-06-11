// Package interference controls GPIO channels used for interference output.
package interference

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	externalGPIOPath = "/sys/external_gpio"

	valueFilePrefix     = "jwsioc_gpio"
	directionFilePrefix = "jwsioc_inout_gpio"

	attributeFilePerm = 0o644

	directionIn  = "in"
	directionOut = "out"

	directionValueIn  = 0
	directionValueOut = 1

	valueLow  = 0
	valueHigh = 1
)

var (
	sysfsRoot       = externalGPIOPath
	writeRetryCount = 5
	writeRetryDelay = 20 * time.Millisecond
)

// GPIOPin is the pin operation interface used by Service.
type GPIOPin interface {
	Setup() error
	SetHigh() error
	SetLow() error
	GetValue() (int, error)
	Cleanup()
}

type gpioDirectionReader interface {
	GetDirection() (string, error)
}

// Pin represents one external GPIO exposed by /sys/external_gpio.
type Pin struct {
	Number        int
	valuePath     string
	directionPath string
}

// NewPin creates an external GPIO pin instance.
func NewPin(number int) *Pin {
	return &Pin{
		Number:        number,
		valuePath:     filepath.Join(sysfsRoot, fmt.Sprintf("%s%d", valueFilePrefix, number)),
		directionPath: filepath.Join(sysfsRoot, fmt.Sprintf("%s%d", directionFilePrefix, number)),
	}
}

// Setup checks the pin and sets it to output mode.
func (p *Pin) Setup() error {
	if err := p.ensureAvailable(); err != nil {
		return err
	}
	return p.SetDirection(directionOut)
}

// SetHigh sets high level.
func (p *Pin) SetHigh() error {
	return p.SetValue(valueHigh)
}

// SetLow sets low level.
func (p *Pin) SetLow() error {
	return p.SetValue(valueLow)
}

// Cleanup keeps the pin low after operation.
func (p *Pin) Cleanup() {
	if !p.isAvailable() {
		return
	}
	_ = p.SetLow()
}

// SetDirection sets pin direction to "in" or "out".
func (p *Pin) SetDirection(dir string) error {
	value, err := directionFileValue(dir)
	if err != nil {
		return err
	}
	return p.writeAttributeWithRetry("direction", strconv.Itoa(value), writeRetryCount, writeRetryDelay)
}

// GetDirection reads current pin direction.
func (p *Pin) GetDirection() (string, error) {
	data, err := p.readAttribute("direction")
	if err != nil {
		return "", err
	}
	switch data {
	case strconv.Itoa(directionValueIn), directionIn:
		return directionIn, nil
	case strconv.Itoa(directionValueOut), directionOut:
		return directionOut, nil
	default:
		return "", fmt.Errorf("解析 IO%d 方向失败: %q", p.Number, data)
	}
}

// SetValue sets pin value to 0 or 1.
func (p *Pin) SetValue(value int) error {
	if value != valueLow && value != valueHigh {
		return fmt.Errorf("无效电平值: %d，仅支持 0/1", value)
	}
	return p.writeAttribute("value", strconv.Itoa(value))
}

// GetValue reads current pin value.
func (p *Pin) GetValue() (int, error) {
	data, err := p.readAttribute("value")
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(data)
	if err != nil {
		return 0, fmt.Errorf("解析 IO%d 电平失败: %w", p.Number, err)
	}
	return value, nil
}

func (p *Pin) isAvailable() bool {
	return statOK(p.valuePath) && statOK(p.directionPath)
}

func (p *Pin) ensureAvailable() error {
	if _, err := os.Stat(p.valuePath); err != nil {
		return fmt.Errorf("外部 IO%d 电平文件不可用: %w", p.Number, err)
	}
	if _, err := os.Stat(p.directionPath); err != nil {
		return fmt.Errorf("外部 IO%d 方向文件不可用: %w", p.Number, err)
	}
	return nil
}

func (p *Pin) attributePath(name string) (string, error) {
	switch name {
	case "value":
		return p.valuePath, nil
	case "direction":
		return p.directionPath, nil
	default:
		return "", fmt.Errorf("未知 IO%d 属性: %s", p.Number, name)
	}
}

func (p *Pin) readAttribute(name string) (string, error) {
	path, err := p.attributePath(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (p *Pin) writeAttribute(name, value string) error {
	path, err := p.attributePath(name)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(value), attributeFilePerm)
}

func (p *Pin) writeAttributeWithRetry(name, value string, retries int, delay time.Duration) error {
	path, err := p.attributePath(name)
	if err != nil {
		return err
	}

	var lastErr error
	for i := range retries {
		lastErr = os.WriteFile(path, []byte(value), attributeFilePerm)
		if lastErr == nil {
			return nil
		}
		if i < retries-1 {
			time.Sleep(delay)
		}
	}
	return lastErr
}

func directionFileValue(dir string) (int, error) {
	switch dir {
	case directionIn:
		return directionValueIn, nil
	case directionOut:
		return directionValueOut, nil
	default:
		return 0, fmt.Errorf("无效方向: %s，仅支持 in/out", dir)
	}
}

func statOK(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ListExternalPins lists external IO numbers currently available.
func ListExternalPins() []int {
	entries, err := os.ReadDir(sysfsRoot)
	if err != nil {
		return []int{}
	}

	values := make(map[int]bool, len(entries))
	directions := make(map[int]bool, len(entries))
	for _, entry := range entries {
		number, kind, ok := parseExternalGPIOName(entry.Name())
		if !ok {
			continue
		}
		switch kind {
		case "value":
			values[number] = true
		case "direction":
			directions[number] = true
		}
	}

	pins := make([]int, 0, len(values))
	for number := range values {
		if directions[number] {
			pins = append(pins, number)
		}
	}
	sort.Ints(pins)
	return pins
}

func parseExternalGPIOName(name string) (int, string, bool) {
	if strings.HasPrefix(name, directionFilePrefix) {
		number, ok := parseExternalNumber(strings.TrimPrefix(name, directionFilePrefix))
		return number, "direction", ok
	}
	if strings.HasPrefix(name, valueFilePrefix) {
		number, ok := parseExternalNumber(strings.TrimPrefix(name, valueFilePrefix))
		return number, "value", ok
	}
	return 0, "", false
}

func parseExternalNumber(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	number, err := strconv.Atoi(raw)
	if err != nil || number < 0 {
		return 0, false
	}
	return number, true
}
