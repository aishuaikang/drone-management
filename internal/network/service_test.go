package network

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	outputs  map[string]string
	errors   map[string]error
	blocks   map[string]bool
	started  []string
	commands []string
}

func (r *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	r.commands = append(r.commands, key)
	if r.blocks[key] {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return r.outputs[key], r.errors[key]
}

func (r *fakeRunner) Start(name string, args ...string) error {
	r.started = append(r.started, strings.Join(append([]string{name}, args...), " "))
	return nil
}

func TestUpdateConfigValidatesAndCreatesBackup(t *testing.T) {
	service, runner := newTestService(t)
	oldConfig := "network:\n  version: 2\n  ethernets: {}\n"
	if err := os.WriteFile(service.configPath, []byte(oldConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	newConfig := "network:\n  version: 2\n  renderer: networkd\n  ethernets:\n    eth0:\n      dhcp4: true\n"

	got, err := service.UpdateConfig(context.Background(), newConfig)
	if err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	if !got.Exists || got.Content != newConfig {
		t.Fatalf("UpdateConfig() = %#v", got)
	}
	data, err := os.ReadFile(service.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != newConfig {
		t.Fatalf("config = %q, want %q", data, newConfig)
	}
	backups, err := service.Backups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %#v, want one backup", backups)
	}
	backupData, err := os.ReadFile(filepath.Join(filepath.Dir(service.configPath), backups[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	if string(backupData) != oldConfig {
		t.Fatalf("backup = %q, want %q", backupData, oldConfig)
	}
	if len(runner.commands) == 0 || runner.commands[0] != "netplan generate" {
		t.Fatalf("commands = %#v", runner.commands)
	}
}

func TestUpdateConfigRollsBackFailedValidation(t *testing.T) {
	service, runner := newTestService(t)
	oldConfig := "network:\n  version: 2\n  ethernets: {}\n"
	if err := os.WriteFile(service.configPath, []byte(oldConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	runner.errors["netplan generate"] = errors.New("invalid YAML")

	_, err := service.UpdateConfig(context.Background(), "network:\n  version: broken\n")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("UpdateConfig() error = %v, want ErrInvalidInput", err)
	}
	data, readErr := os.ReadFile(service.configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != oldConfig {
		t.Fatalf("config after rollback = %q, want %q", data, oldConfig)
	}
}

func TestUpdateConfigRejectsUnsupportedRenderer(t *testing.T) {
	service, runner := newTestService(t)
	oldConfig := "network:\n  version: 2\n  renderer: networkd\n  ethernets: {}\n"
	if err := os.WriteFile(service.configPath, []byte(oldConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := service.UpdateConfig(context.Background(), "network:\n  version: 2\n  renderer: NetworkManager\n  ethernets: {}\n")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("UpdateConfig() error = %v, want ErrInvalidInput", err)
	}
	data, readErr := os.ReadFile(service.configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != oldConfig {
		t.Fatalf("config after rejection = %q, want %q", data, oldConfig)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %#v, want no commands", runner.commands)
	}
}

func TestApplyRequiresManagedConfig(t *testing.T) {
	service, runner := newTestService(t)

	err := service.Apply(context.Background())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Apply() error = %v, want ErrInvalidInput", err)
	}
	if len(runner.commands) != 0 || len(runner.started) != 0 {
		t.Fatalf("runner state = commands %#v, started %#v", runner.commands, runner.started)
	}
}

func TestApplyRejectsUnsupportedManagedRendererBeforeMutatingServices(t *testing.T) {
	service, runner := newTestService(t)
	config := "network:\n  version: 2\n  renderer: NetworkManager\n  ethernets: {}\n"
	if err := os.WriteFile(service.configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	err := service.Apply(context.Background())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Apply() error = %v, want ErrInvalidInput", err)
	}
	if len(runner.commands) != 0 || len(runner.started) != 0 {
		t.Fatalf("runner state = commands %#v, started %#v", runner.commands, runner.started)
	}
}

func TestValidateManagedNetplanIgnoresRendererTextInsideQuotedValues(t *testing.T) {
	content := "network:\n  version: 2\n  renderer: networkd\n  wifis:\n    wlan0:\n      access-points:\n        test:\n          password: 'renderer: NetworkManager'\n"
	if err := validateManagedNetplan(content); err != nil {
		t.Fatalf("validateManagedNetplan() error = %v", err)
	}
}

func TestValidateManagedNetplanRejectsRendererBypasses(t *testing.T) {
	tests := map[string]string{
		"flow mapping":    "network: {version: 2, renderer: NetworkManager}\n",
		"quoted key":      "network:\n  version: 2\n  'renderer': NetworkManager\n",
		"merge alias":     "defaults: &defaults\n  renderer: NetworkManager\nnetwork:\n  version: 2\n  <<: *defaults\n",
		"device renderer": "network:\n  version: 2\n  ethernets:\n    eth0:\n      renderer: NetworkManager\n      dhcp4: true\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validateManagedNetplan(content); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("validateManagedNetplan() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestBackupPathRejectsTraversal(t *testing.T) {
	service, _ := newTestService(t)
	for _, name := range []string{"../99-custom-config.yaml.backup.bad", "other.yaml.backup.bad", ""} {
		if _, err := service.BackupContent(name); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("BackupContent(%q) error = %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestParseInterfacesAndRoutes(t *testing.T) {
	interfaces := parseInterfaces("IFACE:eth0|STATE:up|MAC:00:11:22:33:44:55|IP:192.168.1.2/24|GATEWAY:192.168.1.1|METRIC:100\n")
	if len(interfaces) != 1 || !interfaces[0].IsUp || interfaces[0].Gateway != "192.168.1.1" {
		t.Fatalf("interfaces = %#v", interfaces)
	}
	routes := parseRoutes("default via 192.168.1.1 dev eth0 proto dhcp metric 100\n192.168.1.0/24 dev eth0 proto kernel scope link\n")
	if len(routes) != 2 || routes[0].Interface != "eth0" || routes[0].Metric != "100" || routes[1].Scope != "link" {
		t.Fatalf("routes = %#v", routes)
	}
}

func TestFixDNSRejectsInvalidServer(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.FixDNS(context.Background(), []string{"8.8.8.8; rm -rf /"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("FixDNS() error = %v, want ErrInvalidInput", err)
	}
}

func TestDiagnoseDNSReturnsConfigurationWhenNetworkProbesTimeout(t *testing.T) {
	service, runner := newTestService(t)
	if err := os.WriteFile(service.resolvConfPath, []byte("nameserver 114.114.114.114\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner.outputs["systemctl is-active systemd-resolved"] = "active"
	for _, command := range []string{
		"resolvectl status",
		"resolvectl dns",
		"getent hosts www.baidu.com",
		"ping -c 1 -W 2 8.8.8.8",
	} {
		runner.blocks[command] = true
	}

	startedAt := time.Now()
	result, err := service.DiagnoseDNS(context.Background())
	if err != nil {
		t.Fatalf("DiagnoseDNS() error = %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("DiagnoseDNS() took %s, want bounded probe time", elapsed)
	}
	if result.DNSServers != "nameserver 114.114.114.114" {
		t.Fatalf("DNS servers = %q, want resolv.conf fallback", result.DNSServers)
	}
	if !strings.Contains(result.ResolvectlStatus, "timed out") || !strings.Contains(result.TestResult, "timed out") || !strings.Contains(result.PingTest, "timed out") {
		t.Fatalf("diagnostics = %#v, want stage-specific timeout results", result)
	}
}

func TestFixDNSReturnsBoundedRestartTimeoutAfterWritingConfig(t *testing.T) {
	service, runner := newTestService(t)
	runner.outputs["systemctl is-active systemd-resolved"] = "active"
	runner.blocks["systemctl restart systemd-resolved"] = true

	startedAt := time.Now()
	err := service.FixDNS(context.Background(), []string{"114.114.114.114", "8.8.8.8"})
	if err == nil || !strings.Contains(err.Error(), "restart systemd-resolved") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FixDNS() error = %v, want bounded restart timeout", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("FixDNS() took %s, want bounded restart timeout", elapsed)
	}
	data, readErr := os.ReadFile(filepath.Join(filepath.Dir(service.resolvConfPath), "systemd", "resolved.conf"))
	if readErr != nil {
		t.Fatalf("read resolved.conf: %v", readErr)
	}
	if !strings.Contains(string(data), "DNS=114.114.114.114 8.8.8.8") {
		t.Fatalf("resolved.conf = %q, want requested DNS servers", data)
	}
}

func newTestService(t *testing.T) (*Service, *fakeRunner) {
	t.Helper()
	root := t.TempDir()
	runner := &fakeRunner{outputs: map[string]string{}, errors: map[string]error{}, blocks: map[string]bool{}}
	service := &Service{
		runner:            runner,
		goos:              "linux",
		configPath:        filepath.Join(root, "netplan", "99-custom-config.yaml"),
		netplanDir:        filepath.Join(root, "netplan"),
		cloudConfigPath:   filepath.Join(root, "cloud", "99-disable-network-config.cfg"),
		resolvConfPath:    filepath.Join(root, "resolv.conf"),
		interfacesPath:    filepath.Join(root, "interfaces"),
		dnsStatusTimeout:  20 * time.Millisecond,
		dnsProbeTimeout:   20 * time.Millisecond,
		dnsRestartTimeout: 20 * time.Millisecond,
		now: func() time.Time {
			return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
		},
	}
	if err := os.MkdirAll(filepath.Dir(service.configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	return service, runner
}
