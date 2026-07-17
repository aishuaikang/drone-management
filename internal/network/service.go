// Package network manages the host's netplan configuration and network diagnostics.
package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	defaultNetplanPath = "/etc/netplan/99-custom-config.yaml"
	maxConfigBytes     = 1 << 20
)

var (
	// ErrUnsupported indicates that network management is unavailable on this host.
	ErrUnsupported = errors.New("network management is only available on Linux")
	// ErrInvalidInput indicates that a request contains invalid network configuration.
	ErrInvalidInput = errors.New("invalid network configuration")
	// ErrNotFound indicates that a requested network artifact does not exist.
	ErrNotFound = errors.New("network artifact not found")
)

// Config contains the editable netplan document.
type Config struct {
	Content string `json:"content"`
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
}

// InterfaceStatus describes one physical network interface.
type InterfaceStatus struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	IsUp    bool   `json:"isUp"`
	MAC     string `json:"mac"`
	IP      string `json:"ip"`
	Gateway string `json:"gateway"`
	Metric  string `json:"metric"`
}

// Route describes one IPv4 route.
type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway,omitempty"`
	Metric      string `json:"metric,omitempty"`
	Interface   string `json:"interface,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

// Connectivity reports gateway, internet, and DNS reachability.
type Connectivity struct {
	DefaultGateway    string `json:"defaultGateway"`
	GatewayReachable  bool   `json:"gatewayReachable"`
	InternetReachable bool   `json:"internetReachable"`
	DNSWorking        bool   `json:"dnsWorking"`
}

// DNSDiagnostics reports the active resolver configuration.
type DNSDiagnostics struct {
	ResolvConf       string `json:"resolvConf"`
	SystemdResolved  bool   `json:"systemdResolved"`
	ResolvectlStatus string `json:"resolvectlStatus,omitempty"`
	DNSServers       string `json:"dnsServers"`
	TestResult       string `json:"testResult"`
	PingTest         string `json:"pingTest"`
	ResolvConfLink   string `json:"resolvConfLink"`
}

// ServiceStatus reports the expected and actual state of one network service.
type ServiceStatus struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
	Enabled     bool   `json:"enabled"`
	Masked      bool   `json:"masked"`
	ShouldRun   bool   `json:"shouldRun"`
	IsCorrect   bool   `json:"isCorrect"`
}

// Diagnostics reports network manager conflicts on the host.
type Diagnostics struct {
	CloudInitEnabled      bool            `json:"cloudInitEnabled"`
	NetworkManagerActive  bool            `json:"networkManagerActive"`
	SystemdNetworkdActive bool            `json:"systemdNetworkdActive"`
	IfupdownConfigured    bool            `json:"ifupdownConfigured"`
	NetplanFiles          []string        `json:"netplanFiles"`
	ActiveRenderer        string          `json:"activeRenderer"`
	Conflicts             []string        `json:"conflicts"`
	Recommendations       []string        `json:"recommendations"`
	ServiceStatuses       []ServiceStatus `json:"serviceStatuses"`
}

// Backup identifies one saved netplan document.
type Backup struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	Size      int64     `json:"size"`
}

type commandRunner interface {
	Run(context.Context, string, ...string) (string, error)
	Start(string, ...string) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s: %w", text, err)
		}
		return text, err
	}
	return text, nil
}

func (execRunner) Start(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Service manages networking on the same host as drone-management.
type Service struct {
	runner          commandRunner
	goos            string
	configPath      string
	netplanDir      string
	cloudConfigPath string
	resolvConfPath  string
	interfacesPath  string
	now             func() time.Time
}

// NewService creates a host network management service.
func NewService() *Service {
	return &Service{
		runner:          execRunner{},
		goos:            runtime.GOOS,
		configPath:      defaultNetplanPath,
		netplanDir:      "/etc/netplan",
		cloudConfigPath: "/etc/cloud/cloud.cfg.d/99-disable-network-config.cfg",
		resolvConfPath:  "/etc/resolv.conf",
		interfacesPath:  "/sys/class/net",
		now:             time.Now,
	}
}

// GetConfig returns the managed netplan document or a generated default.
func (s *Service) GetConfig() (Config, error) {
	if err := s.requireLinux(); err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(s.configPath)
	if err == nil {
		return Config{Content: string(data), Path: s.configPath, Exists: true}, nil
	}
	if !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("read netplan config: %w", err)
	}
	return Config{Content: s.defaultConfig(), Path: s.configPath}, nil
}

// UpdateConfig atomically stores and validates the managed netplan document.
func (s *Service) UpdateConfig(ctx context.Context, content string) (Config, error) {
	if err := s.requireLinux(); err != nil {
		return Config{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" || len(content) > maxConfigBytes {
		return Config{}, fmt.Errorf("%w: netplan YAML is empty or too large", ErrInvalidInput)
	}
	if err := validateManagedNetplan(content); err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(s.netplanDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create netplan directory: %w", err)
	}

	previous, readErr := os.ReadFile(s.configPath)
	hadPrevious := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return Config{}, fmt.Errorf("read current netplan config: %w", readErr)
	}
	if hadPrevious {
		if _, err := s.writeBackup(previous); err != nil {
			return Config{}, err
		}
	}
	if err := writeFileAtomic(s.configPath, []byte(content+"\n"), 0o600); err != nil {
		return Config{}, fmt.Errorf("write netplan config: %w", err)
	}
	if _, err := s.runner.Run(ctx, "netplan", "generate"); err != nil {
		if hadPrevious {
			_ = writeFileAtomic(s.configPath, previous, 0o600)
		} else {
			_ = os.Remove(s.configPath)
		}
		return Config{}, fmt.Errorf("%w: netplan validation failed: %v", ErrInvalidInput, err)
	}
	if err := os.MkdirAll(filepath.Dir(s.cloudConfigPath), 0o755); err != nil {
		return Config{}, fmt.Errorf("create cloud-init config directory: %w", err)
	}
	if err := writeFileAtomic(s.cloudConfigPath, []byte("network: {config: disabled}\n"), 0o644); err != nil {
		return Config{}, fmt.Errorf("disable cloud-init network config: %w", err)
	}
	return Config{Content: content + "\n", Path: s.configPath, Exists: true}, nil
}

// Apply prepares systemd-networkd and schedules netplan apply in the background.
func (s *Service) Apply(ctx context.Context) error {
	if err := s.requireLinux(); err != nil {
		return err
	}
	content, err := os.ReadFile(s.configPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("%w: save the managed netplan configuration before applying", ErrInvalidInput)
	}
	if err != nil {
		return fmt.Errorf("read managed netplan config: %w", err)
	}
	if err := validateManagedNetplan(string(content)); err != nil {
		return err
	}
	if _, err := s.runner.Run(ctx, "netplan", "generate"); err != nil {
		return fmt.Errorf("validate netplan config: %w", err)
	}
	s.prepareNetworkManagers(ctx)
	return s.scheduleScript(`#!/bin/sh
exec >>/tmp/drone-management-netplan-apply.log 2>&1
sleep 1
netplan apply
if [ -e /run/systemd/resolve/stub-resolv.conf ]; then
  rm -f /etc/resolv.conf
  ln -s /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
fi
systemctl restart systemd-resolved 2>/dev/null || true
systemctl restart systemd-networkd 2>/dev/null || true
netplan apply 2>/dev/null || true
rm -f -- "$0"
`)
}

// Restart schedules a non-destructive netplan re-apply.
func (s *Service) Restart(ctx context.Context) error {
	if err := s.requireLinux(); err != nil {
		return err
	}
	if _, err := s.runner.Run(ctx, "netplan", "generate"); err != nil {
		return fmt.Errorf("validate netplan config: %w", err)
	}
	return s.scheduleScript(`#!/bin/sh
exec >>/tmp/drone-management-network-restart.log 2>&1
sleep 1
netplan apply || systemctl restart systemd-networkd
rm -f -- "$0"
`)
}

// Interfaces returns physical interface status reported by the kernel.
func (s *Service) Interfaces(ctx context.Context) ([]InterfaceStatus, error) {
	if err := s.requireLinux(); err != nil {
		return nil, err
	}
	output, err := s.runner.Run(ctx, "sh", "-c", interfaceStatusScript)
	if err != nil {
		return nil, fmt.Errorf("read network interfaces: %w", err)
	}
	return parseInterfaces(output), nil
}

// Routes returns the current IPv4 route table.
func (s *Service) Routes(ctx context.Context) ([]Route, error) {
	if err := s.requireLinux(); err != nil {
		return nil, err
	}
	output, err := s.runner.Run(ctx, "ip", "-4", "route", "show")
	if err != nil {
		return nil, fmt.Errorf("read route table: %w", err)
	}
	return parseRoutes(output), nil
}

// TestConnectivity tests the active default gateway, public IP, and DNS.
func (s *Service) TestConnectivity(ctx context.Context) (Connectivity, error) {
	if err := s.requireLinux(); err != nil {
		return Connectivity{}, err
	}
	result := Connectivity{}
	output, _ := s.runner.Run(ctx, "ip", "route", "show", "default")
	line := firstLine(output)
	result.DefaultGateway = line
	fields := strings.Fields(line)
	for index, field := range fields {
		if field == "via" && index+1 < len(fields) {
			_, err := s.runner.Run(ctx, "ping", "-c", "1", "-W", "2", fields[index+1])
			result.GatewayReachable = err == nil
			break
		}
	}
	_, err := s.runner.Run(ctx, "ping", "-c", "1", "-W", "3", "8.8.8.8")
	result.InternetReachable = err == nil
	_, err = s.runner.Run(ctx, "getent", "hosts", "www.baidu.com")
	result.DNSWorking = err == nil
	return result, nil
}

// DiagnoseDNS returns resolver configuration and live lookup results.
func (s *Service) DiagnoseDNS(ctx context.Context) (DNSDiagnostics, error) {
	if err := s.requireLinux(); err != nil {
		return DNSDiagnostics{}, err
	}
	result := DNSDiagnostics{}
	if target, err := os.Readlink(s.resolvConfPath); err == nil {
		result.ResolvConfLink = target
	}
	if data, err := os.ReadFile(s.resolvConfPath); err == nil {
		result.ResolvConf = strings.TrimSpace(string(data))
	}
	output, _ := s.runner.Run(ctx, "systemctl", "is-active", "systemd-resolved")
	result.SystemdResolved = strings.TrimSpace(output) == "active"
	if result.SystemdResolved {
		result.ResolvectlStatus, _ = s.runner.Run(ctx, "resolvectl", "status")
		result.DNSServers, _ = s.runner.Run(ctx, "resolvectl", "dns")
	}
	if result.DNSServers == "" {
		for _, line := range strings.Split(result.ResolvConf, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "nameserver ") {
				result.DNSServers += strings.TrimSpace(line) + "\n"
			}
		}
		result.DNSServers = strings.TrimSpace(result.DNSServers)
	}
	result.TestResult, _ = s.runner.Run(ctx, "getent", "hosts", "www.baidu.com")
	result.PingTest, _ = s.runner.Run(ctx, "ping", "-c", "1", "-W", "2", "www.baidu.com")
	return result, nil
}

// FixDNS configures systemd-resolved or writes resolv.conf directly.
func (s *Service) FixDNS(ctx context.Context, servers []string) error {
	if err := s.requireLinux(); err != nil {
		return err
	}
	if len(servers) == 0 {
		servers = []string{"114.114.114.114", "8.8.8.8"}
	}
	if len(servers) > 4 {
		return fmt.Errorf("%w: at most four DNS servers are allowed", ErrInvalidInput)
	}
	for index := range servers {
		servers[index] = strings.TrimSpace(servers[index])
		if net.ParseIP(servers[index]) == nil {
			return fmt.Errorf("%w: invalid DNS server %q", ErrInvalidInput, servers[index])
		}
	}
	output, _ := s.runner.Run(ctx, "systemctl", "is-active", "systemd-resolved")
	if strings.TrimSpace(output) == "active" {
		resolvedPath := filepath.Join(filepath.Dir(s.resolvConfPath), "systemd", "resolved.conf")
		content := "[Resolve]\nDNS=" + strings.Join(servers, " ") + "\nFallbackDNS=114.114.114.114 8.8.8.8\nDNSStubListener=yes\n"
		if err := writeFileAtomic(resolvedPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write systemd-resolved config: %w", err)
		}
		if _, err := s.runner.Run(ctx, "systemctl", "restart", "systemd-resolved"); err != nil {
			return fmt.Errorf("restart systemd-resolved: %w", err)
		}
		if err := replaceSymlink(s.resolvConfPath, "/run/systemd/resolve/stub-resolv.conf"); err != nil {
			return fmt.Errorf("link resolv.conf: %w", err)
		}
		return nil
	}
	var lines []string
	for _, server := range servers {
		lines = append(lines, "nameserver "+server)
	}
	if err := writeFileAtomic(s.resolvConfPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write resolv.conf: %w", err)
	}
	return nil
}

// Diagnose reports conflicts between netplan and other network managers.
func (s *Service) Diagnose(ctx context.Context) (Diagnostics, error) {
	if err := s.requireLinux(); err != nil {
		return Diagnostics{}, err
	}
	diag := Diagnostics{
		ActiveRenderer:  "networkd",
		Conflicts:       []string{},
		Recommendations: []string{},
		NetplanFiles:    []string{},
		ServiceStatuses: []ServiceStatus{},
	}
	services := []struct {
		name, display string
		shouldRun     bool
	}{
		{"systemd-networkd", "systemd-networkd", true},
		{"systemd-resolved", "systemd-resolved", true},
		{"NetworkManager", "NetworkManager", false},
		{"networking", "ifupdown/networking", false},
		{"connman", "ConnMan", false},
		{"dhcpcd", "dhcpcd", false},
		{"cloud-init", "cloud-init", false},
	}
	for _, item := range services {
		activeOutput, _ := s.runner.Run(ctx, "systemctl", "is-active", item.name)
		enabledOutput, _ := s.runner.Run(ctx, "systemctl", "is-enabled", item.name)
		status := ServiceStatus{
			Name: item.name, DisplayName: item.display, ShouldRun: item.shouldRun,
			Active: strings.TrimSpace(activeOutput) == "active", Enabled: strings.TrimSpace(enabledOutput) == "enabled",
			Masked: strings.TrimSpace(enabledOutput) == "masked",
		}
		if item.shouldRun {
			status.IsCorrect = status.Active && status.Enabled
		} else {
			status.IsCorrect = !status.Active && !status.Enabled
		}
		diag.ServiceStatuses = append(diag.ServiceStatuses, status)
		if item.name == "NetworkManager" {
			diag.NetworkManagerActive = status.Active
		}
		if item.name == "systemd-networkd" {
			diag.SystemdNetworkdActive = status.Active
		}
		if !status.IsCorrect {
			diag.Conflicts = append(diag.Conflicts, "service_state:"+item.name)
		}
	}
	if _, err := os.Stat(s.cloudConfigPath); os.IsNotExist(err) {
		if _, cloudErr := os.Stat(filepath.Join(s.netplanDir, "50-cloud-init.yaml")); cloudErr == nil {
			diag.CloudInitEnabled = true
			diag.Conflicts = append(diag.Conflicts, "cloud_init_managing_network")
		}
	}
	if data, err := os.ReadFile("/etc/network/interfaces"); err == nil && hasLegacyInterfaceConfig(string(data)) {
		diag.IfupdownConfigured = true
		diag.Conflicts = append(diag.Conflicts, "legacy_interfaces_config")
	}
	files, _ := filepath.Glob(filepath.Join(s.netplanDir, "*.yaml"))
	diag.NetplanFiles = files
	if len(files) > 1 {
		diag.Conflicts = append(diag.Conflicts, "multiple_netplan_files")
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), "renderer: NetworkManager") {
			diag.ActiveRenderer = "NetworkManager"
			if !diag.NetworkManagerActive {
				diag.Conflicts = append(diag.Conflicts, "network_manager_not_running")
			}
			break
		}
	}
	if len(diag.Conflicts) == 0 {
		diag.Recommendations = append(diag.Recommendations, "no_conflicts")
	} else {
		diag.Recommendations = append(diag.Recommendations, "apply_managed_netplan")
	}
	return diag, nil
}

// Backups returns the ten most recent managed netplan backups.
func (s *Service) Backups() ([]Backup, error) {
	if err := s.requireLinux(); err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(s.configPath + ".backup.*")
	if err != nil {
		return nil, fmt.Errorf("list netplan backups: %w", err)
	}
	items := make([]Backup, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		items = append(items, Backup{Name: filepath.Base(path), CreatedAt: info.ModTime(), Size: info.Size()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > 10 {
		items = items[:10]
	}
	return items, nil
}

// BackupContent reads one managed backup by name.
func (s *Service) BackupContent(name string) (string, error) {
	path, err := s.backupPath(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read netplan backup: %w", err)
	}
	return string(data), nil
}

// RestoreBackup restores and validates one managed backup.
func (s *Service) RestoreBackup(ctx context.Context, name string) error {
	path, err := s.backupPath(name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read netplan backup: %w", err)
	}
	_, err = s.UpdateConfig(ctx, string(data))
	return err
}

// DeleteBackup deletes one managed backup by name.
func (s *Service) DeleteBackup(name string) error {
	path, err := s.backupPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); os.IsNotExist(err) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("delete netplan backup: %w", err)
	}
	return nil
}

func (s *Service) requireLinux() error {
	if s == nil || s.goos != "linux" {
		return ErrUnsupported
	}
	return nil
}

func validateManagedNetplan(content string) error {
	networkIndent := -1
	versionFound := false

	for _, rawLine := range strings.Split(content, "\n") {
		line := stripYAMLComment(rawLine)
		syntax := maskYAMLQuotedText(line)
		trimmedSyntax := strings.TrimSpace(syntax)
		if trimmedSyntax == "" {
			continue
		}
		indent := len(syntax) - len(strings.TrimLeft(syntax, " \t"))

		if networkIndent < 0 {
			if trimmedSyntax == "network:" {
				if indent != 0 {
					return fmt.Errorf("%w: network must be a top-level mapping", ErrInvalidInput)
				}
				networkIndent = indent
				continue
			}
			if containsYAMLKey(trimmedSyntax, "network") {
				return fmt.Errorf("%w: network must use block mapping syntax", ErrInvalidInput)
			}
			continue
		}

		if indent <= networkIndent {
			break
		}
		if indent == networkIndent+2 && isYAMLKey(trimmedSyntax, "version") {
			if value := yamlScalarValue(strings.TrimSpace(line)); value != "2" {
				return fmt.Errorf("%w: netplan version must be 2", ErrInvalidInput)
			}
			versionFound = true
		}
		if containsYAMLKey(trimmedSyntax, "<<") {
			return fmt.Errorf("%w: YAML merge keys are not supported in managed netplan configuration", ErrInvalidInput)
		}
		if containsQuotedYAMLKey(strings.TrimSpace(line), "renderer") {
			return fmt.Errorf("%w: renderer keys must not be quoted", ErrInvalidInput)
		}
		if isYAMLKey(trimmedSyntax, "renderer") {
			if value := yamlScalarValue(strings.TrimSpace(line)); value != "networkd" {
				return fmt.Errorf("%w: only the networkd renderer is supported", ErrInvalidInput)
			}
			continue
		}
		if containsYAMLKey(trimmedSyntax, "renderer") {
			return fmt.Errorf("%w: renderer must use block mapping syntax", ErrInvalidInput)
		}
	}

	if networkIndent < 0 || !versionFound {
		return fmt.Errorf("%w: netplan YAML must contain a top-level network mapping and version 2", ErrInvalidInput)
	}
	return nil
}

func stripYAMLComment(line string) string {
	quote := byte(0)
	escaped := false
	for index := 0; index < len(line); index++ {
		char := line[index]
		if quote != 0 {
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && char == '\\' {
				escaped = true
				continue
			}
			if char == quote {
				quote = 0
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
		case '#':
			return line[:index]
		}
	}
	return line
}

func maskYAMLQuotedText(line string) string {
	masked := []byte(line)
	quote := byte(0)
	escaped := false
	for index, char := range masked {
		if quote != 0 {
			masked[index] = ' '
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && char == '\\' {
				escaped = true
				continue
			}
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			masked[index] = ' '
		}
	}
	return string(masked)
}

func isYAMLKey(line, key string) bool {
	if !strings.HasPrefix(line, key) {
		return false
	}
	index := len(key)
	for index < len(line) && (line[index] == ' ' || line[index] == '\t') {
		index++
	}
	return index < len(line) && line[index] == ':'
}

func containsYAMLKey(line, key string) bool {
	for offset := 0; offset < len(line); {
		index := strings.Index(line[offset:], key)
		if index < 0 {
			return false
		}
		index += offset
		beforeOK := index == 0 || line[index-1] == ' ' || line[index-1] == '\t' || line[index-1] == '{' || line[index-1] == ','
		after := index + len(key)
		for after < len(line) && (line[after] == ' ' || line[after] == '\t') {
			after++
		}
		if beforeOK && after < len(line) && line[after] == ':' {
			return true
		}
		offset = index + len(key)
	}
	return false
}

func containsQuotedYAMLKey(line, key string) bool {
	for _, quote := range []byte{'\'', '"'} {
		token := string(quote) + key + string(quote)
		for offset := 0; offset < len(line); {
			index := strings.Index(line[offset:], token)
			if index < 0 {
				break
			}
			index += offset
			after := index + len(token)
			for after < len(line) && (line[after] == ' ' || line[after] == '\t') {
				after++
			}
			if after < len(line) && line[after] == ':' {
				return true
			}
			offset = index + len(token)
		}
	}
	return false
}

func yamlScalarValue(line string) string {
	index := strings.IndexByte(line, ':')
	if index < 0 {
		return ""
	}
	value := strings.TrimSpace(line[index+1:])
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	return strings.TrimSpace(value)
}

func (s *Service) defaultConfig() string {
	names := s.interfaceNames()
	if len(names) == 0 {
		names = []string{"eth0"}
	}
	var builder strings.Builder
	builder.WriteString("# /etc/netplan/99-custom-config.yaml\n# Managed by drone-management\nnetwork:\n  version: 2\n  renderer: networkd\n  ethernets:\n")
	for index, name := range names {
		fmt.Fprintf(&builder, "    %s:\n      dhcp4: true\n", name)
		if index > 0 {
			fmt.Fprintf(&builder, "      dhcp4-overrides:\n        route-metric: %d\n", 100+index*100)
		}
		builder.WriteString("      optional: true\n")
	}
	return builder.String()
}

func (s *Service) interfaceNames() []string {
	entries, err := os.ReadDir(s.interfacesPath)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "eth") || strings.HasPrefix(name, "en") || strings.HasPrefix(name, "wl") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 5 {
		names = names[:5]
	}
	return names
}

func (s *Service) prepareNetworkManagers(ctx context.Context) {
	_ = os.MkdirAll(filepath.Join(s.netplanDir, "cloud-init-backup"), 0o755)
	cloudFiles, _ := filepath.Glob(filepath.Join(s.netplanDir, "*cloud-init*.yaml"))
	for _, path := range cloudFiles {
		_ = os.Rename(path, filepath.Join(s.netplanDir, "cloud-init-backup", filepath.Base(path)))
	}
	for _, name := range []string{"NetworkManager", "networking", "connman", "dhcpcd"} {
		_, _ = s.runner.Run(ctx, "systemctl", "stop", name)
		_, _ = s.runner.Run(ctx, "systemctl", "disable", name)
		_, _ = s.runner.Run(ctx, "systemctl", "mask", name)
	}
	for _, name := range []string{"systemd-networkd", "systemd-resolved"} {
		_, _ = s.runner.Run(ctx, "systemctl", "unmask", name)
		_, _ = s.runner.Run(ctx, "systemctl", "enable", name)
		_, _ = s.runner.Run(ctx, "systemctl", "start", name)
	}
}

func (s *Service) scheduleScript(content string) error {
	file, err := os.CreateTemp("/tmp", "drone-management-network-*.sh")
	if err != nil {
		return fmt.Errorf("create network apply script: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	if err := file.Chmod(0o700); err != nil {
		cleanup()
		return fmt.Errorf("chmod network apply script: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		cleanup()
		return fmt.Errorf("write network apply script: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close network apply script: %w", err)
	}
	if err := s.runner.Start("nohup", path); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("start network apply script: %w", err)
	}
	return nil
}

func (s *Service) writeBackup(data []byte) (string, error) {
	stamp := s.now().Format("20060102_150405.000000000")
	path := s.configPath + ".backup." + stamp
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write netplan backup: %w", err)
	}
	return path, nil
}

func (s *Service) backupPath(name string) (string, error) {
	if err := s.requireLinux(); err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	prefix := filepath.Base(s.configPath) + ".backup."
	if name == "" || filepath.Base(name) != name || !strings.HasPrefix(name, prefix) {
		return "", fmt.Errorf("%w: invalid backup name", ErrInvalidInput)
	}
	return filepath.Join(filepath.Dir(s.configPath), name), nil
}

func parseInterfaces(output string) []InterfaceStatus {
	items := []InterfaceStatus{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		item := InterfaceStatus{}
		for _, part := range strings.Split(line, "|") {
			pair := strings.SplitN(part, ":", 2)
			if len(pair) != 2 {
				continue
			}
			switch pair[0] {
			case "IFACE":
				item.Name = pair[1]
			case "STATE":
				item.State = pair[1]
				item.IsUp = pair[1] == "up"
			case "MAC":
				item.MAC = pair[1]
			case "IP":
				item.IP = pair[1]
			case "GATEWAY":
				item.Gateway = pair[1]
			case "METRIC":
				item.Metric = pair[1]
			}
		}
		if item.Name != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseRoutes(output string) []Route {
	items := []Route{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		item := Route{Destination: fields[0]}
		for index := 1; index+1 < len(fields); index++ {
			switch fields[index] {
			case "via":
				item.Gateway = fields[index+1]
			case "dev":
				item.Interface = fields[index+1]
			case "metric":
				item.Metric = fields[index+1]
			case "proto":
				item.Protocol = fields[index+1]
			case "scope":
				item.Scope = fields[index+1]
			}
		}
		items = append(items, item)
	}
	return items
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".drone-management-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func replaceSymlink(path, target string) error {
	if data, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".bak", data, 0o644)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(target, path)
}

func hasLegacyInterfaceConfig(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, " lo") {
			continue
		}
		if strings.HasPrefix(line, "auto ") || strings.HasPrefix(line, "iface ") {
			return true
		}
	}
	return false
}

func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return strings.TrimSpace(value)
}

const interfaceStatusScript = `for iface in $(ls /sys/class/net/ | grep -E '^(eth|en|wl)'); do
state=$(cat /sys/class/net/$iface/operstate 2>/dev/null || echo unknown)
mac=$(cat /sys/class/net/$iface/address 2>/dev/null || echo unknown)
ipaddr=$(ip -4 -o addr show dev $iface 2>/dev/null | awk '{print $4}' | head -1)
[ -n "$ipaddr" ] || ipaddr=NOT_ASSIGNED
gateway=$(ip route show default dev $iface 2>/dev/null | awk '{print $3}' | head -1)
[ -n "$gateway" ] || gateway=NONE
metric=$(ip route show default dev $iface 2>/dev/null | sed -n 's/.* metric \([0-9][0-9]*\).*/\1/p' | head -1)
[ -n "$metric" ] || metric=DEFAULT
echo "IFACE:$iface|STATE:$state|MAC:$mac|IP:$ipaddr|GATEWAY:$gateway|METRIC:$metric"
done`
