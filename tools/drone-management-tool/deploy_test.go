package main

import (
	"strings"
	"testing"
)

func TestValidateReleasePackagePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "linux package", path: "/tmp/drone-management_1_linux_arm64.tar.gz"},
		{name: "windows package", path: "/tmp/drone-management_1_windows_amd64.zip", wantErr: true},
		{name: "darwin package", path: "/tmp/drone-management_1_darwin_arm64.tar.gz", wantErr: true},
		{name: "wrong extension", path: "/tmp/drone-management.tar", wantErr: true},
		{name: "empty", path: " ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateReleasePackagePath(tt.path)
			if tt.wantErr && err == nil {
				t.Fatalf("validateReleasePackagePath(%q) expected error", tt.path)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateReleasePackagePath(%q) unexpected error: %v", tt.path, err)
			}
		})
	}
}

func TestBuildDeployScript(t *testing.T) {
	t.Parallel()

	script := buildDeployScript(
		DeployRequest{InstallDir: "/spbatc/drone-management"},
		"/tmp/drone-management-tool/pkg.tar.gz",
		"/tmp/drone-management-tool-1",
	)
	required := []string{
		"drone-management.service",
		"--warning=no-timestamp",
		`[ -e "$INSTALL_DIR" ] && [ ! -d "$INSTALL_DIR" ]`,
		`mv "$INSTALL_DIR" "$BACKUP_PATH"`,
		"API_ADDR=0.0.0.0:$API_PORT",
		"API_LICENSE_PATH=$INSTALL_DIR/license.lic",
		"API_FPV_VIDEO_MEDIAMTX_PATH=$INSTALL_DIR/MediaMTX",
		`cp -a "$BINARY" "$INSTALL_DIR/$BINARY_NAME"`,
		`cp -a "$PACKAGE_ROOT/MediaMTX" "$INSTALL_DIR/MediaMTX"`,
		"systemctl enable \"$SERVICE_NAME\"",
		"curl -fsS \"$health_url\"",
	}
	for _, needle := range required {
		if !strings.Contains(script, needle) {
			t.Fatalf("deploy script missing %q", needle)
		}
	}
	forbidden := []string{"chromium", "kiosk", "autostart", ".desktop", `cp -a "$PACKAGE_ROOT"/. "$INSTALL_DIR"/`}
	lower := strings.ToLower(script)
	for _, needle := range forbidden {
		if strings.Contains(lower, strings.ToLower(needle)) {
			t.Fatalf("deploy script unexpectedly contains %q", needle)
		}
	}
}
