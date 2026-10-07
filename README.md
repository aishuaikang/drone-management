# Drone Management 网口版

Drone Management 后端启动 HTTP API，并启动两个 TCP server 接收设备数据；ddsT1 定位协议还可按需启用 UDP 接收：

- ddsT1 定位数据：默认 `0.0.0.0:10007`
- ddsT1 定位 UDP：默认关闭，启用后默认 `0.0.0.0:10007`
- A3-F9 FPV 告警数据：默认 `0.0.0.0:10005`

部署电脑网口 IP 固定为 `192.168.100.101`，设备端把 TCP server 目标地址配置为该 IP。

定位接收兼容 `RID_GB46750` 的 18 字段标准格式和带机型的 19 字段扩展格式；`dji_O,4` 原始空口数据同时兼容 176 字节与 180 字节，接收和解密链路保持报文实际长度，不提前截取。

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

默认会在 `dist/` 下生成 Linux ARM64、Windows AMD64、macOS ARM64 软件包。只构建指定平台：

```bash
scripts/build-release.sh linux/arm64 windows/amd64
VERSION=2.2.6 TARGETS="linux/arm64" scripts/build-release.sh
```

## 配置

“关于”页面的生产厂家固定为“深圳市特信电子有限公司”；使用厂家、使用人员默认留空，由 Drone Management Tool 的“软件信息”维护。Linux 设备连接 SSH 后，在安装目录下读取、编辑并保存；Windows 本机程序可在 Tool 登录界面点击“本机软件信息”，选择程序目录后设置，无需 SSH。

Linux 远程保存通过 SSH 原子替换信息文件，无需 SFTP 的 POSIX rename 扩展。安装目录由 root 管理时，普通 SSH 账号须具备非交互 sudo 权限；权限不足时保存会报错并保留原信息。

这两项信息独立保存在程序运行目录的 `data/about.json`，格式为 `{"userCompany":"","userName":""}`。主程序仅提供 `GET /api/v1/about` 读取，不通过用户设置接口修改。保存后刷新“关于”页面即可生效，无需重启。自定义 `API_ABOUT_INFO_PATH` 时，须确保维护工具写入的文件与后端读取路径一致。

参考 `.env.example`。常用配置：

- `API_ADDR`：HTTP API 地址，默认 `:18080`
- `API_ABOUT_INFO_PATH`：“关于”页面的使用厂家、使用人员信息文件，默认 `./data/about.json`
- `API_TCP_BIND_HOST`：TCP 监听地址，默认 `0.0.0.0`
- `API_POSITION_TCP_PORT`：定位数据端口，默认 `10007`
- `API_POSITION_UDP_ENABLED`：是否同时接收 ddsT1 UDP 定位报文，默认 `false`
- `API_POSITION_UDP_PORT`：ddsT1 UDP 监听端口，默认 `10007`；与 TCP 端口相互独立
- `API_FPV_TCP_PORT`：FPV 告警端口，默认 `10005`
- `API_FPV_COMMAND_TIMEOUT_MS`：FPV AT 指令等待 `OK` 回执超时，默认 `3000`
- `API_FPV_VIDEO_RTSP_URL`：FPV 图传 RTSP 地址，默认 `rtsp://192.168.100.106:554/live/1_1`
- `API_FPV_VIDEO_MEDIAMTX_PATH`：内置 MediaMTX 二进制目录，默认 `./MediaMTX`
- `API_FPV_VIDEO_MEDIAMTX_WORK_DIR`：MediaMTX 临时配置目录，默认 `./tmp/fpv-video`
- `API_FPV_VIDEO_MEDIAMTX_BIN`：手动指定 MediaMTX 二进制路径，默认按平台从 `MediaMTX/` 自动选择
- `API_FPV_VIDEO_INTERNAL_RTSP_PORT`：RTMP 推流启用时内部 RTSP/TCP 代理端口，默认 `18554`，仅监听 `127.0.0.1`
- `API_FPV_VIDEO_WEBRTC_HOST` / `API_FPV_VIDEO_WEBRTC_PORT` / `API_FPV_VIDEO_WEBRTC_UDP_PORT`：MediaMTX WebRTC 监听配置，默认 `127.0.0.1:18889` 和 UDP `18189`
- `API_FPV_VIDEO_WHEP_URL`：外部 WHEP 地址；设置后后端不启动内置 MediaMTX，前端通过后端 WHEP 代理自实现 WebRTC 播放
- `API_FPV_VIDEO_RECORD_DB_PATH`：FPV 图传录制记录数据库，默认 `./data/fpv-videos.db`
- `API_FPV_VIDEO_RECORD_DIR`：FPV 图传录制视频目录，默认 `./data/fpv-videos`
- `API_O3_DECRYPT_ENABLED`：是否启用 `dji_O,4` 的 O3/O4 DID MQTT 联网解密，默认启用
- `API_O3_DECRYPT_BROKER` / `API_O3_DECRYPT_PORT` / `API_O3_DECRYPT_USERNAME` / `API_O3_DECRYPT_PASSWORD`：联网解密 MQTT 服务配置
- `API_O3_DECRYPT_TIMEOUT_MS` / `API_O3_DECRYPT_CONNECT_TIMEOUT_MS`：解密请求和 MQTT 连接超时

`MediaMTX/` 目录用于存放不同平台的 MediaMTX。发布脚本会按目标平台复制对应文件，例如 `mediamtx_v1.19.0_linux_arm64`、`mediamtx_v1.19.0_windows_amd64.exe`、`mediamtx_v1.19.0_darwin_arm64`。

在“设置 → RTMP 推流”中可配置一个 `rtmp://` 或 `rtmps://` 地址。默认关闭且地址为空；只有打开 FPV 视频会话时才推流，关闭会话即停止。后端内置纯 Go 发布器，只复制 H.264/H.265 视频轨，不推音频、不转码，因此上游视频和目标服务必须支持相同编码。RTMP 连接失败会自动重试，不影响本地 WebRTC 播放和录像；发布包无需额外携带 FFmpeg。
