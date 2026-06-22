package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type DeployRequest struct {
	InstallDir         string `json:"installDir"`
	ReleasePackagePath string `json:"releasePackagePath"`
}

type DeployResult struct {
	InstallDir string `json:"installDir"`
	Message    string `json:"message"`
}

func (a *App) DeployDroneManagement(req DeployRequest) (DeployResult, error) {
	req.InstallDir = a.getInstallDir(req.InstallDir)
	req.ReleasePackagePath = strings.TrimSpace(req.ReleasePackagePath)
	if req.ReleasePackagePath == "" {
		return DeployResult{}, fmt.Errorf("请选择发布包")
	}
	if err := validateReleasePackagePath(req.ReleasePackagePath); err != nil {
		return DeployResult{}, err
	}
	info, err := os.Stat(req.ReleasePackagePath)
	if err != nil {
		return DeployResult{}, fmt.Errorf("读取发布包失败: %w", err)
	}
	if info.IsDir() {
		return DeployResult{}, fmt.Errorf("发布包不能是目录")
	}
	if _, err := a.getSSHClient(); err != nil {
		return DeployResult{}, err
	}
	webrtcHost := webRTCHostFromSSHHost(a.connectedSSHHost())

	a.updateConfig(func(cfg *AppConfig) {
		cfg.InstallDir = req.InstallDir
		cfg.ReleasePackage = req.ReleasePackagePath
	})

	taskDir := fmt.Sprintf("/tmp/drone-management-tool-%d", time.Now().UnixNano())
	remotePackage := remoteJoin(taskDir, filepath.Base(req.ReleasePackagePath))
	a.emitProgress("deploy-progress", 0, "准备部署", "正在创建远程临时目录", "running", 0, nil)
	if _, err := a.runCommand("mkdir -p " + shellQuote(taskDir)); err != nil {
		a.emitProgress("deploy-progress", 0, "准备部署", "创建远程临时目录失败", "error", 0, err)
		return DeployResult{}, err
	}
	a.emitProgress("deploy-progress", 0, "准备部署", "远程临时目录已创建", "success", 100, nil)

	a.emitProgress("deploy-progress", 1, "上传发布包", "正在上传 "+filepath.Base(req.ReleasePackagePath), "running", 0, nil)
	if err := a.uploadFile(req.ReleasePackagePath, remotePackage, func(read, total int64) {
		progress := 0
		if total > 0 {
			progress = int(float64(read) / float64(total) * 100)
		}
		a.emitProgress(
			"deploy-progress",
			1,
			"上传发布包",
			fmt.Sprintf("已上传 %s / %s", formatBytes(read), formatBytes(total)),
			"running",
			progress,
			nil,
		)
	}); err != nil {
		a.emitProgress("deploy-progress", 1, "上传发布包", "上传失败", "error", 0, err)
		return DeployResult{}, err
	}
	a.emitProgress("deploy-progress", 1, "上传发布包", "上传完成", "success", 100, nil)

	a.emitProgress("deploy-progress", 2, "安装服务", "正在安装并写入开机自启动", "running", 20, nil)
	script := buildDeployScript(req, remotePackage, taskDir, webrtcHost)
	output, err := a.runCommand(script)
	if err != nil {
		wrapped := fmt.Errorf("%w%s", err, commandOutputSuffix(output))
		a.emitProgress("deploy-progress", 2, "安装服务", "安装失败", "error", 20, wrapped)
		return DeployResult{}, wrapped
	}
	a.emitProgress("deploy-progress", 2, "安装服务", "服务已安装并设置自启动", "success", 100, nil)
	a.emitProgress("deploy-progress", 3, "启动校验", "健康检查通过", "success", 100, nil)
	return DeployResult{InstallDir: req.InstallDir, Message: "部署完成，服务已设置为开机自启动"}, nil
}

func buildDeployScript(req DeployRequest, remotePackage, taskDir string, webrtcHost string) string {
	return fmt.Sprintf(`set -eu
REMOTE_PACKAGE=%s
INSTALL_DIR=%s
TASK_DIR=%s
PREFERRED_WEBRTC_HOST=%s
BINARY_NAME=drone-management
SERVICE_NAME=drone-management.service
API_PORT=18080
SUDO=
if [ "$(id -u)" != "0" ]; then
  SUDO=sudo
fi
detect_webrtc_host() {
  if command -v ip >/dev/null 2>&1; then
    detected="$(ip route get 1.1.1.1 2>/dev/null | awk '{for (i=1; i<=NF; i++) if ($i=="src") {print $(i+1); exit}}')"
    if [ -n "$detected" ]; then
      echo "$detected"
      return
    fi
  fi
  hostname -I 2>/dev/null | awk '{print $1}'
}
WEBRTC_HOST="$PREFERRED_WEBRTC_HOST"
if [ -z "$WEBRTC_HOST" ]; then
  WEBRTC_HOST="$(detect_webrtc_host)"
fi
if [ -z "$WEBRTC_HOST" ]; then
  WEBRTC_HOST=0.0.0.0
fi
cleanup() {
  rm -rf "$TASK_DIR"
}
trap cleanup EXIT
if ! command -v systemctl >/dev/null 2>&1; then
  echo "设备未安装 systemctl" >&2
  exit 1
fi
if ! command -v tar >/dev/null 2>&1; then
  echo "设备未安装 tar" >&2
  exit 1
fi
extract_release_package() {
  if tar --version 2>/dev/null | grep -qi 'gnu tar'; then
    tar --warning=no-unknown-keyword --warning=no-timestamp -xzf "$REMOTE_PACKAGE" -C "$EXTRACT_DIR"
  else
    tar -xzf "$REMOTE_PACKAGE" -C "$EXTRACT_DIR"
  fi
}
wait_for_backend() {
  health_url="http://127.0.0.1:$API_PORT/healthz"
  if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
    echo "未检测到 curl 或 wget，跳过健康检查命令" >&2
    return 0
  fi
  for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    if command -v curl >/dev/null 2>&1; then
      curl -fsS "$health_url" >/dev/null 2>&1 && return 0
    else
      wget -q -O /dev/null "$health_url" >/dev/null 2>&1 && return 0
    fi
    sleep 1
  done
  return 1
}

EXTRACT_DIR="$TASK_DIR/extract"
rm -rf "$EXTRACT_DIR"
mkdir -p "$EXTRACT_DIR"
extract_release_package
BINARY="$(find "$EXTRACT_DIR" -type f -name "$BINARY_NAME" | head -n 1)"
if [ -z "$BINARY" ]; then
  echo "发布包中未找到 drone-management 可执行文件" >&2
  exit 1
fi
PACKAGE_ROOT="$(dirname "$BINARY")"

$SUDO systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
if [ -e "$INSTALL_DIR" ] && [ ! -d "$INSTALL_DIR" ]; then
  BACKUP_PATH="${INSTALL_DIR}.backup.$(date +%%Y%%m%%d%%H%%M%%S)"
  $SUDO mv "$INSTALL_DIR" "$BACKUP_PATH"
  echo "备份旧安装文件: $BACKUP_PATH"
fi
$SUDO mkdir -p "$INSTALL_DIR"
if [ -d "$INSTALL_DIR" ] && [ "$(find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 2>/dev/null | head -n 1)" ]; then
  BACKUP_DIR="${INSTALL_DIR}.backup.$(date +%%Y%%m%%d%%H%%M%%S)"
  $SUDO cp -a "$INSTALL_DIR" "$BACKUP_DIR"
  echo "备份目录: $BACKUP_DIR"
fi

$SUDO rm -f "$INSTALL_DIR/$BINARY_NAME"
$SUDO rm -rf "$INSTALL_DIR/MediaMTX" "$INSTALL_DIR/README.md" "$INSTALL_DIR/.env.example" "$INSTALL_DIR/协议"
$SUDO cp -a "$BINARY" "$INSTALL_DIR/$BINARY_NAME"
if [ -d "$PACKAGE_ROOT/MediaMTX" ]; then
  $SUDO cp -a "$PACKAGE_ROOT/MediaMTX" "$INSTALL_DIR/MediaMTX"
fi
$SUDO mkdir -p \
  "$INSTALL_DIR/data" \
  "$INSTALL_DIR/data/fpv-videos" \
  "$INSTALL_DIR/static/map" \
  "$INSTALL_DIR/tmp/fpv-video"
$SUDO chmod 0755 "$INSTALL_DIR/$BINARY_NAME"
if [ -d "$INSTALL_DIR/MediaMTX" ]; then
  $SUDO find "$INSTALL_DIR/MediaMTX" -type f -exec chmod 0755 {} \; || true
fi
$SUDO chown -R root:root "$INSTALL_DIR" || true

cat <<SERVICE_EOF | $SUDO tee /etc/systemd/system/drone-management.service >/dev/null
[Unit]
Description=Drone Management Backend
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
Environment=API_ADDR=0.0.0.0:$API_PORT
Environment=API_TCP_BIND_HOST=0.0.0.0
Environment=API_MANUAL_DEVICE_LOCATION_PATH=$INSTALL_DIR/data/manual-device-location.json
Environment=API_INTRUSION_DB_PATH=$INSTALL_DIR/data/intrusions.db
Environment=API_INTERFERENCE_REPORT_DB_PATH=$INSTALL_DIR/data/interference-reports.db
Environment=API_FPV_VIDEO_RECORD_DB_PATH=$INSTALL_DIR/data/fpv-videos.db
Environment=API_USER_SETTINGS_PATH=$INSTALL_DIR/data/user-settings.json
Environment=API_LICENSE_PATH=$INSTALL_DIR/license.lic
Environment=API_OFFLINE_MAP_PATH=$INSTALL_DIR/static/map
Environment=API_FPV_VIDEO_MEDIAMTX_PATH=$INSTALL_DIR/MediaMTX
Environment=API_FPV_VIDEO_MEDIAMTX_WORK_DIR=$INSTALL_DIR/tmp/fpv-video
Environment=API_FPV_VIDEO_WEBRTC_HOST=$WEBRTC_HOST
Environment=API_FPV_VIDEO_WEBRTC_PORT=18889
Environment=API_FPV_VIDEO_WEBRTC_UDP_PORT=18189
Environment=API_FPV_VIDEO_RECORD_DIR=$INSTALL_DIR/data/fpv-videos
ExecStart=$INSTALL_DIR/drone-management
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
SERVICE_EOF

$SUDO systemctl daemon-reload
$SUDO systemctl enable "$SERVICE_NAME" >/dev/null
$SUDO systemctl restart "$SERVICE_NAME"
if ! wait_for_backend; then
  $SUDO systemctl status "$SERVICE_NAME" --no-pager || true
  echo "后端健康检查未就绪" >&2
  exit 1
fi
echo "Installed: $INSTALL_DIR/$BINARY_NAME"
echo "Service enabled: $(systemctl is-enabled "$SERVICE_NAME")"
echo "Service status: $(systemctl is-active "$SERVICE_NAME")"
`, shellQuote(remotePackage),
		shellQuote(req.InstallDir),
		shellQuote(taskDir),
		shellQuote(webrtcHost),
	)
}

func (a *App) connectedSSHHost() string {
	a.sshMu.Lock()
	defer a.sshMu.Unlock()
	if a.conn == nil {
		return ""
	}
	return a.conn.config.Host
}

func webRTCHostFromSSHHost(host string) string {
	host = strings.TrimSpace(host)
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	return host
}

func validateReleasePackagePath(path string) error {
	path = strings.TrimSpace(path)
	name := strings.ToLower(filepath.Base(path))
	if name == "" || name == "." {
		return fmt.Errorf("请选择发布包")
	}
	if strings.Contains(name, "darwin") || strings.Contains(name, "windows") {
		return fmt.Errorf("请选择 Linux 发布包，不能部署 %s", filepath.Base(path))
	}
	if !strings.HasSuffix(name, ".tar.gz") {
		return fmt.Errorf("发布包格式无效，请选择 .tar.gz 文件")
	}
	return nil
}

func commandOutputSuffix(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	return ": " + output
}

func formatBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	size := float64(value)
	for _, unit := range units {
		size /= 1024
		if size < 1024 {
			return fmt.Sprintf("%.1f %s", size, unit)
		}
	}
	return fmt.Sprintf("%.1f PB", size/1024)
}
