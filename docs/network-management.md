# 网络管理

`drone-management` 的网络管理页面用于维护服务所在设备的 Netplan 配置。该功能只在 Linux 上可用，并要求服务进程具备 root 权限；项目部署脚本生成的 systemd 服务已使用 `User=root`。

## 管理范围

- 托管配置文件：`/etc/netplan/99-custom-config.yaml`
- cloud-init 网络禁用文件：`/etc/cloud/cloud.cfg.d/99-disable-network-config.cfg`
- 配置备份：`/etc/netplan/99-custom-config.yaml.backup.<时间戳>`
- 网络后端：Netplan + `systemd-networkd`
- DNS：优先使用 `systemd-resolved`，不可用时直接维护 `/etc/resolv.conf`

页面提供物理网卡、IPv4 路由和连通性状态，以及可视化/YAML 配置、DNS 诊断、网络服务冲突诊断和最近十份配置备份管理。

可视化编辑器只处理能够无损往返的 `networkd` 以太网配置。Wi-Fi、网桥、Bond、VLAN、IPv6 或其他高级 Netplan 字段会自动保留在 YAML 模式，页面不会用简化配置覆盖它们。

## 保存与应用

保存配置时，后端依次执行：

1. 备份当前托管配置。
2. 原子写入新配置并设置权限为 `0600`。
3. 执行 `netplan generate` 校验完整 Netplan 配置。
4. 校验失败时恢复原配置；校验成功后禁用 cloud-init 网络接管。

页面中的“应用配置”会先保存并校验当前草稿；保存失败时不会修改网络服务。后端也会拒绝应用尚未创建的托管配置，并只接受 `renderer: networkd`。校验通过后，系统会把可能冲突的 `NetworkManager`、`networking`、`connman` 和 `dhcpcd` 服务停用并屏蔽，启用 `systemd-networkd` 与 `systemd-resolved`，随后在后台执行 `netplan apply`。如果 IP、网关或默认路由发生变化，当前浏览器连接可能中断，需要使用新地址重新访问平台。

## HTTP API

所有网络管理接口位于 `/api/v1/network`，并沿用平台授权校验：

- `GET|PUT /config`：读取或保存托管配置
- `POST /apply`：后台应用配置
- `POST /restart`：后台重新应用 Netplan
- `GET /interfaces`、`GET /routes`、`GET /connectivity`：运行状态
- `GET|PUT /dns`：DNS 诊断与修复
- `GET /diagnostics`：网络管理服务冲突诊断
- `GET /backups`：备份列表
- `GET|DELETE /backups/{name}`：读取或删除备份
- `POST /backups/{name}/restore`：恢复并校验备份

非 Linux 环境返回 `501 network_unsupported`，服务未装配时返回 `503 network_unavailable`。
