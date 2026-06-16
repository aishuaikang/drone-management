# Drone Management 网口版

Drone Management 后端启动 HTTP API，并启动两个 TCP server 接收设备数据：

- ddsT1 定位数据：默认 `0.0.0.0:10007`
- A3-F9 FPV 告警数据：默认 `0.0.0.0:10005`

部署电脑网口 IP 固定为 `192.168.100.101`，设备端把 TCP server 目标地址配置为该 IP。

## 开发运行

```bash
go run ./cmd/api
```

前端开发：

```bash
cd frontend
npm install
npm run dev
```

生产构建：

```bash
cd frontend && npm install
cd ..
scripts/build-release.sh
```

默认会在 `dist/` 下生成 Linux、Windows、macOS 的 `amd64/arm64` 软件包。只构建指定平台：

```bash
scripts/build-release.sh linux/arm64 windows/amd64
VERSION=2.2.6 TARGETS="linux/arm64" scripts/build-release.sh
```

## 配置

参考 `.env.example`。常用配置：

- `API_ADDR`：HTTP API 地址，默认 `:18080`
- `API_TCP_BIND_HOST`：TCP 监听地址，默认 `0.0.0.0`
- `API_POSITION_TCP_PORT`：定位数据端口，默认 `10007`
- `API_FPV_TCP_PORT`：FPV 告警端口，默认 `10005`
- `API_FPV_COMMAND_TIMEOUT_MS`：FPV AT 指令等待 `OK` 回执超时，默认 `3000`
- `API_FPV_VIDEO_RTSP_URL`：FPV 图传 RTSP 地址，默认 `rtsp://192.168.100.106:554/live/1_1`
- `API_FPV_VIDEO_MEDIAMTX_PATH`：内置 MediaMTX 二进制目录，默认 `./MediaMTX`
- `API_FPV_VIDEO_MEDIAMTX_WORK_DIR`：MediaMTX 临时配置目录，默认 `./tmp/fpv-video`
- `API_FPV_VIDEO_MEDIAMTX_BIN`：手动指定 MediaMTX 二进制路径，默认按平台从 `MediaMTX/` 自动选择
- `API_FPV_VIDEO_WEBRTC_HOST` / `API_FPV_VIDEO_WEBRTC_PORT` / `API_FPV_VIDEO_WEBRTC_UDP_PORT`：MediaMTX WebRTC 监听配置，默认 `127.0.0.1:18889` 和 UDP `18189`
- `API_FPV_VIDEO_WHEP_URL`：外部 WHEP 地址；设置后后端不启动内置 MediaMTX，前端通过后端 WHEP 代理自实现 WebRTC 播放
- `API_FPV_VIDEO_RECORD_DB_PATH`：FPV 图传录制记录数据库，默认 `./data/fpv-videos.db`
- `API_FPV_VIDEO_RECORD_DIR`：FPV 图传录制视频目录，默认 `./data/fpv-videos`
- `API_O3_DECRYPT_ENABLED`：是否启用 `dji_O,4` 的 O3/O4 DID MQTT 联网解密，默认启用
- `API_O3_DECRYPT_BROKER` / `API_O3_DECRYPT_PORT` / `API_O3_DECRYPT_USERNAME` / `API_O3_DECRYPT_PASSWORD`：联网解密 MQTT 服务配置
- `API_O3_DECRYPT_TIMEOUT_MS` / `API_O3_DECRYPT_CONNECT_TIMEOUT_MS`：解密请求和 MQTT 连接超时

`MediaMTX/` 目录用于存放不同平台的 MediaMTX。发布脚本会按目标平台复制对应文件，例如 `mediamtx_v1.19.0_linux_arm64`、`mediamtx_v1.19.0_windows_amd64.exe`、`mediamtx_v1.19.0_darwin_arm64`。
