import {
  AlertTriangle,
  CheckCircle2,
  CircleX,
  Code2,
  FileClock,
  Gauge,
  Network,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
  Settings2,
  Trash2,
  Wrench,
  Wifi,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import {
  applyNetworkConfig,
  deleteNetworkBackup,
  fixNetworkDNS,
  getNetworkBackupContent,
  getNetworkBackups,
  getNetworkConfig,
  getNetworkDNSDiagnostics,
  getNetworkDiagnostics,
  getNetworkInterfaces,
  getNetworkRoutes,
  restartNetwork,
  restoreNetworkBackup,
  testNetworkConnectivity,
  updateNetworkConfig,
} from "../api";
import type {
  NetworkBackup,
  NetworkConnectivity,
  NetworkDNSDiagnostics,
  NetworkDiagnostics,
  NetworkInterfaceStatus,
  NetworkRoute,
} from "../types";

type Locale = "zh-CN" | "en-US";
type Page = "overview" | "configuration" | "diagnostics" | "backups";
type EditorMode = "visual" | "yaml";
type Notice = { tone: "success" | "error"; text: string } | null;
type NetplanParseResult = { configs: InterfaceConfig[]; visualSafe: boolean };

type AddressConfig = { ip: string; netmask: string };
type StaticRouteConfig = { to: string; via: string; metric: number };
type InterfaceConfig = {
  name: string;
  dhcp4: boolean;
  addresses: AddressConfig[];
  gateway: string;
  metric: number;
  dns: string[];
  staticRoutes: StaticRouteConfig[];
  optional: boolean;
};

const copy = {
  "zh-CN": {
    title: "网络管理",
    overview: "运行状态",
    configuration: "Netplan 配置",
    diagnostics: "诊断",
    backups: "备份",
    refresh: "刷新",
    interfaceStatus: "网络接口",
    routeTable: "路由表",
    noInterfaces: "未检测到物理网卡",
    noRoutes: "暂无路由",
    interface: "接口",
    status: "状态",
    address: "IP 地址",
    gateway: "网关",
    metric: "优先级 Metric",
    destination: "目标网段",
    protocol: "协议",
    up: "已连接",
    down: "未连接",
    visual: "可视化",
    yaml: "YAML",
    addInterface: "添加网卡",
    remove: "删除",
    dhcp: "自动获取 IPv4",
    optional: "可选接口",
    staticAddresses: "静态地址",
    ip: "IP",
    netmask: "子网掩码",
    dns: "DNS 服务器",
    staticRoutes: "静态路由",
    via: "下一跳",
    addAddress: "添加地址",
    addDNS: "添加 DNS",
    addRoute: "添加路由",
    save: "保存配置",
    apply: "应用配置",
    restart: "重启网络",
    saved: "网络配置已保存并通过语法校验",
    applyQueued: "当前配置已保存并进入后台应用队列",
    restartQueued: "网络重启已进入后台队列",
    applyConfirm: "将先保存并校验当前配置，再应用到本机；这可能改变 IP 并中断当前连接，确认继续？",
    restartConfirm: "重启网络可能短暂中断当前连接，确认继续？",
    configPath: "配置文件",
    newConfig: "尚未创建，将保存为托管配置",
    connectivity: "连通性",
    testConnectivity: "开始测试",
    defaultGateway: "默认路由",
    gatewayReachable: "网关可达",
    internetReachable: "互联网可达",
    dnsWorking: "DNS 解析",
    passed: "正常",
    failed: "异常",
    pending: "未测试",
    networkConflicts: "网络服务冲突",
    runDiagnostics: "运行诊断",
    renderer: "渲染器",
    cloudInit: "cloud-init",
    legacyConfig: "传统 interfaces",
    services: "系统服务",
    expected: "期望",
    actual: "当前",
    enabled: "启用",
    disabled: "禁用",
    active: "运行中",
    inactive: "未运行",
    noConflicts: "未发现配置冲突",
    dnsDiagnostics: "DNS 配置",
    diagnoseDNS: "读取 DNS 状态",
    dnsServers: "DNS 地址，使用逗号分隔",
    fixDNS: "写入 DNS",
    dnsUpdated: "DNS 配置已更新",
    dnsUpdatedRefreshFailed: "DNS 已写入，但状态刷新失败",
    resolvConf: "/etc/resolv.conf",
    resolverStatus: "解析器状态",
    dnsLookupResult: "DNS 查询",
    internetProbe: "互联网探测",
    backupName: "备份文件",
    createdAt: "创建时间",
    size: "大小",
    preview: "预览",
    restore: "恢复",
    delete: "删除",
    noBackups: "暂无配置备份",
    restoreConfirm: "恢复此备份并执行语法校验？",
    deleteConfirm: "永久删除此备份？",
    restored: "备份已恢复并通过语法校验",
    deleted: "备份已删除",
    closePreview: "关闭预览",
    loading: "正在读取网络状态",
    invalidInterface: "网卡名称不能为空",
    unsupportedVisualConfig: "当前 YAML 包含可视化编辑器不支持的配置，请继续使用 YAML 模式",
    loadFailed: "读取网络管理数据失败",
  },
  "en-US": {
    title: "Network Management",
    overview: "Status",
    configuration: "Netplan",
    diagnostics: "Diagnostics",
    backups: "Backups",
    refresh: "Refresh",
    interfaceStatus: "Interfaces",
    routeTable: "Routes",
    noInterfaces: "No physical interfaces detected",
    noRoutes: "No routes",
    interface: "Interface",
    status: "Status",
    address: "IP address",
    gateway: "Gateway",
    metric: "Route metric",
    destination: "Destination",
    protocol: "Protocol",
    up: "Up",
    down: "Down",
    visual: "Visual",
    yaml: "YAML",
    addInterface: "Add interface",
    remove: "Remove",
    dhcp: "Automatic IPv4",
    optional: "Optional interface",
    staticAddresses: "Static addresses",
    ip: "IP",
    netmask: "Netmask",
    dns: "DNS servers",
    staticRoutes: "Static routes",
    via: "Next hop",
    addAddress: "Add address",
    addDNS: "Add DNS",
    addRoute: "Add route",
    save: "Save",
    apply: "Apply",
    restart: "Restart network",
    saved: "Configuration saved and validated",
    applyQueued: "The current configuration was saved and scheduled for apply",
    restartQueued: "Network restart was scheduled",
    applyConfirm: "The current configuration will be saved and validated before apply. This may change the host IP and interrupt the connection. Continue?",
    restartConfirm: "Restarting may briefly interrupt the connection. Continue?",
    configPath: "Configuration file",
    newConfig: "Not created yet; save to create the managed configuration",
    connectivity: "Connectivity",
    testConnectivity: "Run test",
    defaultGateway: "Default route",
    gatewayReachable: "Gateway",
    internetReachable: "Internet",
    dnsWorking: "DNS",
    passed: "Healthy",
    failed: "Failed",
    pending: "Not tested",
    networkConflicts: "Network manager conflicts",
    runDiagnostics: "Run diagnostics",
    renderer: "Renderer",
    cloudInit: "cloud-init",
    legacyConfig: "Legacy interfaces",
    services: "Services",
    expected: "Expected",
    actual: "Actual",
    enabled: "Enabled",
    disabled: "Disabled",
    active: "Active",
    inactive: "Inactive",
    noConflicts: "No configuration conflicts found",
    dnsDiagnostics: "DNS configuration",
    diagnoseDNS: "Read DNS status",
    dnsServers: "DNS addresses separated by commas",
    fixDNS: "Write DNS",
    dnsUpdated: "DNS configuration updated",
    dnsUpdatedRefreshFailed: "DNS was updated, but status refresh failed",
    resolvConf: "/etc/resolv.conf",
    resolverStatus: "Resolver status",
    dnsLookupResult: "DNS lookup",
    internetProbe: "Internet probe",
    backupName: "Backup",
    createdAt: "Created",
    size: "Size",
    preview: "Preview",
    restore: "Restore",
    delete: "Delete",
    noBackups: "No configuration backups",
    restoreConfirm: "Restore and validate this backup?",
    deleteConfirm: "Permanently delete this backup?",
    restored: "Backup restored and validated",
    deleted: "Backup deleted",
    closePreview: "Close preview",
    loading: "Loading network status",
    invalidInterface: "Interface name is required",
    unsupportedVisualConfig: "This YAML contains settings that the visual editor cannot preserve; continue in YAML mode",
    loadFailed: "Failed to load network management data",
  },
} as const;

export function NetworkManagement({ locale }: { locale: Locale }) {
  const t = copy[locale];
  const [page, setPage] = useState<Page>("overview");
  const [editorMode, setEditorMode] = useState<EditorMode>("visual");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState<Notice>(null);
  const [configPath, setConfigPath] = useState("");
  const [configExists, setConfigExists] = useState(false);
  const [yamlContent, setYamlContent] = useState("");
  const [configDirty, setConfigDirty] = useState(false);
  const [interfaceConfigs, setInterfaceConfigs] = useState<InterfaceConfig[]>([]);
  const [interfaces, setInterfaces] = useState<NetworkInterfaceStatus[]>([]);
  const [routes, setRoutes] = useState<NetworkRoute[]>([]);
  const [connectivity, setConnectivity] = useState<NetworkConnectivity | null>(null);
  const [dnsDiagnostics, setDNSDiagnostics] = useState<NetworkDNSDiagnostics | null>(null);
  const [diagnostics, setDiagnostics] = useState<NetworkDiagnostics | null>(null);
  const [dnsServers, setDNSServers] = useState("114.114.114.114, 8.8.8.8");
  const [backups, setBackups] = useState<NetworkBackup[]>([]);
  const [preview, setPreview] = useState<{ name: string; content: string } | null>(null);

  const showError = useCallback((error: unknown, fallback = t.loadFailed) => {
    setNotice({ tone: "error", text: error instanceof Error ? error.message : fallback });
  }, [t.loadFailed]);

  const loadConfig = useCallback(async () => {
    const result = await getNetworkConfig();
    setConfigPath(result.path);
    setConfigExists(result.exists);
    setYamlContent(result.content);
    setConfigDirty(false);
    const parsed = parseNetplanConfig(result.content);
    setInterfaceConfigs(parsed.configs);
    setEditorMode(parsed.visualSafe && parsed.configs.length > 0 ? "visual" : "yaml");
  }, []);

  const loadOverview = useCallback(async () => {
    const [interfaceResult, routeResult] = await Promise.all([getNetworkInterfaces(), getNetworkRoutes()]);
    setInterfaces(interfaceResult.items ?? []);
    setRoutes(routeResult.items ?? []);
  }, []);

  const loadBackups = useCallback(async () => {
    const result = await getNetworkBackups();
    setBackups(result.items ?? []);
  }, []);

  const refreshAll = useCallback(async () => {
    setLoading(true);
    setNotice(null);
    try {
      await Promise.all([loadConfig(), loadOverview(), loadBackups()]);
    } catch (error) {
      showError(error);
    } finally {
      setLoading(false);
    }
  }, [loadBackups, loadConfig, loadOverview, showError]);

  useEffect(() => {
    void refreshAll();
  }, [refreshAll]);

  const generatedYaml = useMemo(() => generateNetplanConfig(interfaceConfigs), [interfaceConfigs]);

  const handleEditorMode = (nextMode: EditorMode) => {
    if (nextMode === editorMode) return;
    if (nextMode === "yaml") {
      setYamlContent(generatedYaml);
    } else {
      const parsed = parseNetplanConfig(yamlContent);
      if (!parsed.visualSafe) {
        setNotice({ tone: "error", text: t.unsupportedVisualConfig });
        return;
      }
      if (parsed.configs.length === 0) {
        setNotice({ tone: "error", text: t.invalidInterface });
        return;
      }
      setInterfaceConfigs(parsed.configs);
    }
    setEditorMode(nextMode);
  };

  const handleSave = async () => {
    const content = editorMode === "visual" ? generatedYaml : yamlContent;
    if (editorMode === "visual" && interfaceConfigs.some((item) => !item.name.trim())) {
      setNotice({ tone: "error", text: t.invalidInterface });
      return;
    }
    setBusy("save");
    setNotice(null);
    try {
      const result = await updateNetworkConfig(content);
      setYamlContent(result.content);
      setConfigDirty(false);
      setConfigPath(result.path);
      setConfigExists(true);
      setNotice({ tone: "success", text: t.saved });
      await loadBackups();
    } catch (error) {
      showError(error);
    } finally {
      setBusy("");
    }
  };

  const handleApply = async () => {
    const content = editorMode === "visual" ? generatedYaml : yamlContent;
    if (editorMode === "visual" && interfaceConfigs.some((item) => !item.name.trim())) {
      setNotice({ tone: "error", text: t.invalidInterface });
      return;
    }
    if (!window.confirm(t.applyConfirm)) return;
    setBusy("apply");
    setNotice(null);
    try {
      if (!configExists || configDirty) {
        const result = await updateNetworkConfig(content);
        setYamlContent(result.content);
        setConfigDirty(false);
        setConfigPath(result.path);
        setConfigExists(true);
        await loadBackups();
      }
      await applyNetworkConfig();
      setNotice({ tone: "success", text: t.applyQueued });
    } catch (error) {
      showError(error);
    } finally {
      setBusy("");
    }
  };

  const handleRestart = async () => {
    if (!window.confirm(t.restartConfirm)) return;
    setBusy("restart");
    setNotice(null);
    try {
      await restartNetwork();
      setNotice({ tone: "success", text: t.restartQueued });
    } catch (error) {
      showError(error);
    } finally {
      setBusy("");
    }
  };

  const handleConnectivity = async () => {
    setBusy("connectivity");
    try {
      setConnectivity(await testNetworkConnectivity());
    } catch (error) {
      showError(error);
    } finally {
      setBusy("");
    }
  };

  const handleDiagnostics = async () => {
    setBusy("diagnostics");
    try {
      setDiagnostics(await getNetworkDiagnostics());
    } catch (error) {
      showError(error);
    } finally {
      setBusy("");
    }
  };

  const handleDNSDiagnostics = async () => {
    setBusy("dns-diagnostics");
    try {
      setDNSDiagnostics(await getNetworkDNSDiagnostics());
    } catch (error) {
      showError(error);
    } finally {
      setBusy("");
    }
  };

  const handleFixDNS = async () => {
    setBusy("dns-fix");
    try {
      await fixNetworkDNS(dnsServers.split(",").map((item) => item.trim()).filter(Boolean));
      setNotice({ tone: "success", text: t.dnsUpdated });
      void getNetworkDNSDiagnostics()
        .then(setDNSDiagnostics)
        .catch((error) => {
          const detail = error instanceof Error ? error.message : "";
          setNotice({
            tone: "success",
            text: detail ? `${t.dnsUpdatedRefreshFailed}: ${detail}` : t.dnsUpdatedRefreshFailed,
          });
        });
    } catch (error) {
      showError(error);
    } finally {
      setBusy("");
    }
  };

  const handlePreview = async (name: string) => {
    setBusy(`preview:${name}`);
    try {
      const result = await getNetworkBackupContent(name);
      setPreview({ name, content: result.content });
    } catch (error) {
      showError(error);
    } finally {
      setBusy("");
    }
  };

  const handleRestore = async (name: string) => {
    if (!window.confirm(t.restoreConfirm)) return;
    setBusy(`restore:${name}`);
    try {
      await restoreNetworkBackup(name);
      setNotice({ tone: "success", text: t.restored });
      setPreview(null);
      await Promise.all([loadConfig(), loadBackups()]);
    } catch (error) {
      showError(error);
    } finally {
      setBusy("");
    }
  };

  const handleDelete = async (name: string) => {
    if (!window.confirm(t.deleteConfirm)) return;
    setBusy(`delete:${name}`);
    try {
      await deleteNetworkBackup(name);
      setNotice({ tone: "success", text: t.deleted });
      if (preview?.name === name) setPreview(null);
      await loadBackups();
    } catch (error) {
      showError(error);
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="network-management">
      <header className="network-management__header">
        <div>
          <Network aria-hidden="true" />
          <h2>{t.title}</h2>
          <span>{configPath || "netplan"}</span>
        </div>
        <button type="button" onClick={() => void refreshAll()} disabled={loading || Boolean(busy)} title={t.refresh}>
          <RefreshCw size={15} aria-hidden="true" className={loading ? "network-spin" : ""} />
          {t.refresh}
        </button>
      </header>

      <div className="network-management__tabs" role="tablist">
        {([
          ["overview", t.overview, <Gauge size={15} aria-hidden="true" />],
          ["configuration", t.configuration, <Settings2 size={15} aria-hidden="true" />],
          ["diagnostics", t.diagnostics, <Wrench size={15} aria-hidden="true" />],
          ["backups", t.backups, <FileClock size={15} aria-hidden="true" />],
        ] as Array<[Page, string, React.ReactNode]>).map(([id, label, icon]) => (
          <button key={id} type="button" role="tab" aria-selected={page === id} className={page === id ? "active" : ""} onClick={() => setPage(id)}>
            {icon}<span>{label}</span>
          </button>
        ))}
      </div>

      {notice ? (
        <div className={`network-notice network-notice--${notice.tone}`} role="status">
          {notice.tone === "success" ? <CheckCircle2 size={16} aria-hidden="true" /> : <AlertTriangle size={16} aria-hidden="true" />}
          <span>{notice.text}</span>
          <button type="button" onClick={() => setNotice(null)} title="Close"><X size={14} aria-hidden="true" /></button>
        </div>
      ) : null}

      <div className="network-management__body">
        {loading ? (
          <div className="network-empty"><RefreshCw className="network-spin" aria-hidden="true" /><span>{t.loading}</span></div>
        ) : page === "overview" ? (
          <OverviewPage t={t} interfaces={interfaces} routes={routes} connectivity={connectivity} busy={busy} onConnectivity={handleConnectivity} />
        ) : page === "configuration" ? (
          <ConfigurationPage
            t={t}
            mode={editorMode}
            configExists={configExists}
            yaml={yamlContent}
            configs={interfaceConfigs}
            statuses={interfaces}
            busy={busy}
            onMode={handleEditorMode}
            onYAML={(value) => {
              setYamlContent(value);
              setConfigDirty(true);
            }}
            onConfigs={(value) => {
              setInterfaceConfigs(value);
              setConfigDirty(true);
            }}
            onSave={handleSave}
            onApply={handleApply}
            onRestart={handleRestart}
          />
        ) : page === "diagnostics" ? (
          <DiagnosticsPage
            t={t}
            diagnostics={diagnostics}
            dns={dnsDiagnostics}
            dnsServers={dnsServers}
            busy={busy}
            onDNSServers={setDNSServers}
            onDiagnostics={handleDiagnostics}
            onDNSDiagnostics={handleDNSDiagnostics}
            onFixDNS={handleFixDNS}
          />
        ) : (
          <BackupsPage t={t} locale={locale} backups={backups} preview={preview} busy={busy} onPreview={handlePreview} onRestore={handleRestore} onDelete={handleDelete} onClosePreview={() => setPreview(null)} />
        )}
      </div>
    </div>
  );
}

function OverviewPage({ t, interfaces, routes, connectivity, busy, onConnectivity }: {
  t: typeof copy[Locale];
  interfaces: NetworkInterfaceStatus[];
  routes: NetworkRoute[];
  connectivity: NetworkConnectivity | null;
  busy: string;
  onConnectivity: () => void;
}) {
  return (
    <div className="network-overview">
      <section className="network-section network-section--interfaces">
        <div className="network-section__heading"><Wifi aria-hidden="true" /><h3>{t.interfaceStatus}</h3><strong>{interfaces.length}</strong></div>
        {interfaces.length ? <div className="network-interface-grid">{interfaces.map((item) => (
          <article className="network-interface-status" key={item.name}>
            <div className="network-interface-status__title"><i className={item.isUp ? "up" : ""} /><strong>{item.name}</strong><span>{item.isUp ? t.up : t.down}</span></div>
            <dl>
              <div><dt>{t.address}</dt><dd>{item.ip}</dd></div>
              <div><dt>MAC</dt><dd>{item.mac}</dd></div>
              <div><dt>{t.gateway}</dt><dd>{item.gateway}</dd></div>
              <div><dt>{t.metric}</dt><dd>{item.metric}</dd></div>
            </dl>
          </article>
        ))}</div> : <div className="network-empty"><CircleX aria-hidden="true" /><span>{t.noInterfaces}</span></div>}
      </section>

      <section className="network-section network-section--connectivity">
        <div className="network-section__heading"><Gauge aria-hidden="true" /><h3>{t.connectivity}</h3><button type="button" onClick={onConnectivity} disabled={busy === "connectivity"}><RefreshCw size={14} className={busy === "connectivity" ? "network-spin" : ""} />{t.testConnectivity}</button></div>
        <div className="network-connectivity-grid">
          <HealthValue label={t.gatewayReachable} value={connectivity?.gatewayReachable} t={t} />
          <HealthValue label={t.internetReachable} value={connectivity?.internetReachable} t={t} />
          <HealthValue label={t.dnsWorking} value={connectivity?.dnsWorking} t={t} />
        </div>
        <div className="network-default-route"><span>{t.defaultGateway}</span><code>{connectivity?.defaultGateway || "-"}</code></div>
      </section>

      <section className="network-section network-section--routes">
        <div className="network-section__heading"><Network aria-hidden="true" /><h3>{t.routeTable}</h3><strong>{routes.length}</strong></div>
        <div className="network-table-wrap"><table className="network-table"><thead><tr><th>{t.destination}</th><th>{t.gateway}</th><th>{t.interface}</th><th>{t.metric}</th><th>{t.protocol}</th></tr></thead>
          <tbody>{routes.length ? routes.map((route, index) => <tr key={`${route.destination}-${route.interface}-${index}`}><td><code>{route.destination}</code></td><td>{route.gateway || "-"}</td><td>{route.interface || "-"}</td><td>{route.metric || "-"}</td><td>{route.protocol || route.scope || "-"}</td></tr>) : <tr><td colSpan={5}>{t.noRoutes}</td></tr>}</tbody>
        </table></div>
      </section>
    </div>
  );
}

function HealthValue({ label, value, t }: { label: string; value: boolean | undefined; t: typeof copy[Locale] }) {
  return <div className={value === undefined ? "network-health" : value ? "network-health network-health--ok" : "network-health network-health--bad"}>
    {value === undefined ? <Gauge aria-hidden="true" /> : value ? <CheckCircle2 aria-hidden="true" /> : <CircleX aria-hidden="true" />}
    <span>{label}</span><strong>{value === undefined ? t.pending : value ? t.passed : t.failed}</strong>
  </div>;
}

function ConfigurationPage({ t, mode, configExists, yaml, configs, statuses, busy, onMode, onYAML, onConfigs, onSave, onApply, onRestart }: {
  t: typeof copy[Locale]; mode: EditorMode; configExists: boolean; yaml: string; configs: InterfaceConfig[]; statuses: NetworkInterfaceStatus[]; busy: string;
  onMode: (mode: EditorMode) => void; onYAML: (value: string) => void; onConfigs: (value: InterfaceConfig[]) => void;
  onSave: () => void; onApply: () => void; onRestart: () => void;
}) {
  const update = (index: number, patch: Partial<InterfaceConfig>) => onConfigs(configs.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item));
  const addInterface = () => {
    let number = 0;
    let name = "eth0";
    while (configs.some((item) => item.name === name)) name = `eth${++number}`;
    onConfigs([...configs, { name, dhcp4: true, addresses: [], gateway: "", metric: configs.length ? 200 : 100, dns: [], staticRoutes: [], optional: true }]);
  };
  return <div className="network-config-page">
    <div className="network-config-toolbar">
      <div className="network-segmented">
        <button type="button" className={mode === "visual" ? "active" : ""} onClick={() => onMode("visual")}><Settings2 size={14} />{t.visual}</button>
        <button type="button" className={mode === "yaml" ? "active" : ""} onClick={() => onMode("yaml")}><Code2 size={14} />{t.yaml}</button>
      </div>
      <span className={configExists ? "network-config-state network-config-state--saved" : "network-config-state"}>{configExists ? t.configPath : t.newConfig}</span>
      {mode === "visual" ? <button type="button" onClick={addInterface}><Plus size={14} />{t.addInterface}</button> : null}
    </div>
    {mode === "yaml" ? <textarea className="network-yaml-editor" value={yaml} onChange={(event) => onYAML(event.target.value)} spellCheck={false} /> : (
      <div className="network-interface-editor-list">{configs.map((config, index) => (
        <InterfaceEditor key={`${index}-${config.name}`} t={t} config={config} status={statuses.find((item) => item.name === config.name)} onChange={(patch) => update(index, patch)} onRemove={() => onConfigs(configs.filter((_, itemIndex) => itemIndex !== index))} />
      ))}</div>
    )}
    <div className="network-config-actions">
      <button type="button" onClick={onRestart} disabled={Boolean(busy)}><RotateCcw size={14} />{t.restart}</button>
      <button type="button" onClick={onSave} disabled={Boolean(busy)}><Save size={14} />{t.save}</button>
      <button type="button" className="primary" onClick={onApply} disabled={Boolean(busy)}><Wifi size={14} />{t.apply}</button>
    </div>
  </div>;
}

function InterfaceEditor({ t, config, status, onChange, onRemove }: { t: typeof copy[Locale]; config: InterfaceConfig; status?: NetworkInterfaceStatus; onChange: (patch: Partial<InterfaceConfig>) => void; onRemove: () => void }) {
  const addresses = config.addresses;
  const routes = config.staticRoutes;
  return <article className="network-interface-editor">
    <header>
      <i className={status?.isUp ? "up" : ""} />
      <input value={config.name} onChange={(event) => onChange({ name: event.target.value.replace(/\s/g, "") })} aria-label={t.interface} />
      <span>{status ? `${status.ip} · ${status.isUp ? t.up : t.down}` : "-"}</span>
      <button type="button" onClick={onRemove} title={t.remove}><Trash2 size={15} /></button>
    </header>
    <div className="network-interface-editor__toggles">
      <label><span>{t.dhcp}</span><input type="checkbox" checked={config.dhcp4} onChange={(event) => onChange({ dhcp4: event.target.checked })} /></label>
      <label><span>{t.optional}</span><input type="checkbox" checked={config.optional} onChange={(event) => onChange({ optional: event.target.checked })} /></label>
      <label><span>{t.metric}</span><input type="number" min={0} value={config.metric} onChange={(event) => onChange({ metric: Number(event.target.value) || 0 })} /></label>
    </div>
    {!config.dhcp4 ? <EditorGroup title={t.staticAddresses} action={t.addAddress} onAdd={() => onChange({ addresses: [...addresses, { ip: "", netmask: "255.255.255.0" }] })}>
      {addresses.map((address, index) => <EditorRow key={index} onRemove={() => onChange({ addresses: addresses.filter((_, itemIndex) => itemIndex !== index) })}>
        <label><span>{t.ip}</span><input value={address.ip} placeholder="192.168.1.10" onChange={(event) => onChange({ addresses: addresses.map((item, itemIndex) => itemIndex === index ? { ...item, ip: event.target.value } : item) })} /></label>
        <label><span>{t.netmask}</span><input value={address.netmask} placeholder="255.255.255.0" onChange={(event) => onChange({ addresses: addresses.map((item, itemIndex) => itemIndex === index ? { ...item, netmask: event.target.value } : item) })} /></label>
      </EditorRow>)}
      <label className="network-editor-field"><span>{t.gateway}</span><input value={config.gateway} placeholder="192.168.1.1" onChange={(event) => onChange({ gateway: event.target.value })} /></label>
    </EditorGroup> : null}
    <EditorGroup title={t.dns} action={t.addDNS} onAdd={() => onChange({ dns: [...config.dns, ""] })}>
      {config.dns.map((dns, index) => <EditorRow key={index} onRemove={() => onChange({ dns: config.dns.filter((_, itemIndex) => itemIndex !== index) })}>
        <label className="wide"><span>DNS</span><input value={dns} placeholder="8.8.8.8" onChange={(event) => onChange({ dns: config.dns.map((item, itemIndex) => itemIndex === index ? event.target.value : item) })} /></label>
      </EditorRow>)}
    </EditorGroup>
    <EditorGroup title={t.staticRoutes} action={t.addRoute} onAdd={() => onChange({ staticRoutes: [...routes, { to: "", via: "", metric: 100 }] })}>
      {routes.map((route, index) => <EditorRow key={index} onRemove={() => onChange({ staticRoutes: routes.filter((_, itemIndex) => itemIndex !== index) })}>
        <label><span>{t.destination}</span><input value={route.to} placeholder="10.0.0.0/8" onChange={(event) => onChange({ staticRoutes: routes.map((item, itemIndex) => itemIndex === index ? { ...item, to: event.target.value } : item) })} /></label>
        <label><span>{t.via}</span><input value={route.via} placeholder="192.168.1.1" onChange={(event) => onChange({ staticRoutes: routes.map((item, itemIndex) => itemIndex === index ? { ...item, via: event.target.value } : item) })} /></label>
        <label><span>{t.metric}</span><input type="number" min={0} value={route.metric} onChange={(event) => onChange({ staticRoutes: routes.map((item, itemIndex) => itemIndex === index ? { ...item, metric: Number(event.target.value) || 0 } : item) })} /></label>
      </EditorRow>)}
    </EditorGroup>
  </article>;
}

function EditorGroup({ title, action, onAdd, children }: { title: string; action: string; onAdd: () => void; children: React.ReactNode }) {
  return <section className="network-editor-group"><div><h4>{title}</h4><button type="button" onClick={onAdd}><Plus size={13} />{action}</button></div>{children}</section>;
}

function EditorRow({ children, onRemove }: { children: React.ReactNode; onRemove: () => void }) {
  return <div className="network-editor-row">{children}<button type="button" onClick={onRemove} title="Remove"><Trash2 size={14} /></button></div>;
}

function DiagnosticsPage({ t, diagnostics, dns, dnsServers, busy, onDNSServers, onDiagnostics, onDNSDiagnostics, onFixDNS }: {
  t: typeof copy[Locale]; diagnostics: NetworkDiagnostics | null; dns: NetworkDNSDiagnostics | null; dnsServers: string; busy: string;
  onDNSServers: (value: string) => void; onDiagnostics: () => void; onDNSDiagnostics: () => void; onFixDNS: () => void;
}) {
  return <div className="network-diagnostics-grid">
    <section className="network-section">
      <div className="network-section__heading"><Wrench aria-hidden="true" /><h3>{t.networkConflicts}</h3><button type="button" onClick={onDiagnostics} disabled={busy === "diagnostics"}><RefreshCw size={14} className={busy === "diagnostics" ? "network-spin" : ""} />{t.runDiagnostics}</button></div>
      {diagnostics ? <>
        <div className="network-diagnostic-summary">
          <HealthValue label={t.cloudInit} value={!diagnostics.cloudInitEnabled} t={t} />
          <HealthValue label={t.legacyConfig} value={!diagnostics.ifupdownConfigured} t={t} />
          <div className="network-health network-health--neutral"><Settings2 /><span>{t.renderer}</span><strong>{diagnostics.activeRenderer}</strong></div>
        </div>
        <div className={diagnostics.conflicts.length ? "network-conflicts" : "network-conflicts network-conflicts--ok"}>{diagnostics.conflicts.length ? diagnostics.conflicts.map((item) => <span key={item}><AlertTriangle size={13} />{formatConflict(item)}</span>) : <span><CheckCircle2 size={13} />{t.noConflicts}</span>}</div>
        <h4 className="network-subheading">{t.services}</h4>
        <div className="network-table-wrap"><table className="network-table"><thead><tr><th>{t.services}</th><th>{t.expected}</th><th>{t.actual}</th><th>{t.status}</th></tr></thead><tbody>{diagnostics.serviceStatuses.map((service) => <tr key={service.name}><td>{service.displayName}</td><td>{service.shouldRun ? t.enabled : t.disabled}</td><td>{service.active ? t.active : service.masked ? `${t.disabled} (masked)` : t.inactive}</td><td>{service.isCorrect ? <CheckCircle2 className="network-table-ok" /> : <AlertTriangle className="network-table-bad" />}</td></tr>)}</tbody></table></div>
      </> : <div className="network-empty"><Gauge /><span>{t.pending}</span></div>}
    </section>
    <section className="network-section">
      <div className="network-section__heading"><Wifi aria-hidden="true" /><h3>{t.dnsDiagnostics}</h3><button type="button" onClick={onDNSDiagnostics} disabled={busy === "dns-diagnostics"}><RefreshCw size={14} className={busy === "dns-diagnostics" ? "network-spin" : ""} />{t.diagnoseDNS}</button></div>
      <div className="network-dns-actions"><input value={dnsServers} onChange={(event) => onDNSServers(event.target.value)} placeholder={t.dnsServers} /><button type="button" onClick={onFixDNS} disabled={busy === "dns-fix"}><Save size={14} />{t.fixDNS}</button></div>
      {dns ? <div className="network-dns-output">
        <dl>
          <div><dt>systemd-resolved</dt><dd>{dns.systemdResolved ? t.active : t.inactive}</dd></div>
          <div><dt>{t.dns}</dt><dd><code>{dns.dnsServers || "-"}</code></dd></div>
          <div><dt>{t.resolverStatus}</dt><dd>{dns.resolvectlStatus || "-"}</dd></div>
          <div><dt>{t.dnsLookupResult}</dt><dd>{dns.testResult || "-"}</dd></div>
          <div><dt>{t.internetProbe}</dt><dd>{dns.pingTest || "-"}</dd></div>
        </dl>
        <label><span>{t.resolvConf}</span><textarea readOnly value={dns.resolvConf || ""} /></label>
      </div> : <div className="network-empty"><Gauge /><span>{t.pending}</span></div>}
    </section>
  </div>;
}

function BackupsPage({ t, locale, backups, preview, busy, onPreview, onRestore, onDelete, onClosePreview }: {
  t: typeof copy[Locale]; locale: Locale; backups: NetworkBackup[]; preview: { name: string; content: string } | null; busy: string;
  onPreview: (name: string) => void; onRestore: (name: string) => void; onDelete: (name: string) => void; onClosePreview: () => void;
}) {
  return <div className={preview ? "network-backups network-backups--preview" : "network-backups"}>
    <section className="network-section">
      <div className="network-section__heading"><FileClock aria-hidden="true" /><h3>{t.backups}</h3><strong>{backups.length}</strong></div>
      <div className="network-table-wrap"><table className="network-table"><thead><tr><th>{t.backupName}</th><th>{t.createdAt}</th><th>{t.size}</th><th /></tr></thead><tbody>{backups.length ? backups.map((backup) => <tr key={backup.name}><td><code>{backup.name}</code></td><td>{new Date(backup.createdAt).toLocaleString(locale, { hour12: false })}</td><td>{formatBytes(backup.size)}</td><td><div className="network-table-actions"><button type="button" onClick={() => onPreview(backup.name)} disabled={busy === `preview:${backup.name}`}><Code2 size={13} />{t.preview}</button><button type="button" onClick={() => onRestore(backup.name)} disabled={Boolean(busy)}><RotateCcw size={13} />{t.restore}</button><button type="button" className="danger" onClick={() => onDelete(backup.name)} disabled={Boolean(busy)}><Trash2 size={13} />{t.delete}</button></div></td></tr>) : <tr><td colSpan={4}>{t.noBackups}</td></tr>}</tbody></table></div>
    </section>
    {preview ? <aside className="network-backup-preview"><header><strong>{preview.name}</strong><button type="button" onClick={onClosePreview} title={t.closePreview}><X size={15} /></button></header><textarea readOnly value={preview.content} /></aside> : null}
  </div>;
}

function parseNetplanConfig(content: string): NetplanParseResult {
  const configs: InterfaceConfig[] = [];
  let current: InterfaceConfig | null = null;
  let topSection = "";
  let section = "";
  let route: StaticRouteConfig | null = null;
  let nameserverAddresses = false;
  let foundNetwork = false;
  let foundVersion = false;
  let foundEthernets = false;
  let visualSafe = true;
  const commitRoute = () => {
    if (!current || !route) return;
    if (route.to || route.via) {
      if (!isIPv4RouteDestination(route.to) || !isIPv4Address(route.via)) visualSafe = false;
      current.staticRoutes.push(route);
    }
    route = null;
  };
  for (const rawLine of content.split("\n")) {
    const uncommented = rawLine.replace(/\s+#.*$/, "").replace(/\t/g, "  ");
    const trimmed = uncommented.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const indent = uncommented.search(/\S/);

    if (indent === 0) {
      commitRoute();
      current = null;
      topSection = "";
      section = "";
      nameserverAddresses = false;
      if (trimmed === "network:" && !foundNetwork) {
        foundNetwork = true;
      } else {
        visualSafe = false;
      }
      continue;
    }

    if (!foundNetwork) {
      visualSafe = false;
      continue;
    }
    if (indent === 2) {
      commitRoute();
      current = null;
      section = "";
      nameserverAddresses = false;
      const [key, ...rest] = trimmed.split(":");
      const value = stripYAMLValue(rest.join(":"));
      if (key === "version") {
        foundVersion = value === "2";
        if (!foundVersion) visualSafe = false;
        topSection = "";
      } else if (key === "renderer") {
        if (value !== "networkd") visualSafe = false;
        topSection = "";
      } else if (trimmed === "ethernets:") {
        foundEthernets = true;
        topSection = "ethernets";
      } else {
        topSection = "";
        visualSafe = false;
      }
      continue;
    }

    if (topSection !== "ethernets") {
      visualSafe = false;
      continue;
    }
    if (indent === 4) {
      commitRoute();
      section = "";
      nameserverAddresses = false;
      const interfaceMatch = trimmed.match(/^([A-Za-z0-9_.:-]+):$/);
      if (!interfaceMatch) {
        current = null;
        visualSafe = false;
        continue;
      }
      current = { name: interfaceMatch[1], dhcp4: false, addresses: [], gateway: "", metric: 0, dns: [], staticRoutes: [], optional: false };
      configs.push(current);
      continue;
    }

    if (!current) {
      visualSafe = false;
      continue;
    }
    if (indent === 6) {
      commitRoute();
      nameserverAddresses = false;
      if (trimmed === "addresses:") section = "addresses";
      else if (trimmed === "routes:") section = "routes";
      else if (trimmed === "nameservers:") section = "nameservers";
      else if (trimmed === "dhcp4-overrides:") section = "dhcp-overrides";
      else {
        section = "";
        const [key, ...rest] = trimmed.split(":");
        const value = stripYAMLValue(rest.join(":"));
        if (key === "dhcp4" && (value === "true" || value === "false")) current.dhcp4 = value === "true";
        else if (key === "optional" && (value === "true" || value === "false")) current.optional = value === "true";
        else if (key === "gateway4" && isIPv4Address(value)) current.gateway = value;
        else visualSafe = false;
      }
      continue;
    }

    if (section === "addresses" && indent === 8 && trimmed.startsWith("-")) {
      const address = parseIPv4CIDR(stripYAMLValue(trimmed.slice(1)));
      if (address) current.addresses.push(address);
      else visualSafe = false;
    } else if (section === "nameservers" && indent === 8 && trimmed === "addresses:") {
      nameserverAddresses = true;
    } else if (section === "nameservers" && indent === 10 && nameserverAddresses && trimmed.startsWith("-")) {
      const server = stripYAMLValue(trimmed.slice(1));
      if (server) current.dns.push(server);
      else visualSafe = false;
    } else if (section === "dhcp-overrides" && indent === 8 && trimmed.startsWith("route-metric:")) {
      const metric = Number(stripYAMLValue(trimmed.split(":").slice(1).join(":")));
      if (Number.isInteger(metric) && metric >= 0) current.metric = metric;
      else visualSafe = false;
    } else if (section === "routes") {
      if (indent === 8 && trimmed.startsWith("-")) {
        commitRoute();
        route = { to: "", via: "", metric: 0 };
        if (!applyRouteField(route, trimmed.slice(1))) visualSafe = false;
      } else if (indent === 10 && route) {
        if (!applyRouteField(route, trimmed)) visualSafe = false;
      } else visualSafe = false;
    } else visualSafe = false;
  }
  commitRoute();
  const finalized = configs.map(finalizeParsedInterface);
  const representable = finalized.every((config) => !config.dhcp4 || (config.addresses.length === 0 && !config.gateway));
  return {
    configs: finalized,
    visualSafe: visualSafe && representable && foundNetwork && foundVersion && foundEthernets,
  };
}

function finalizeParsedInterface(config: InterfaceConfig): InterfaceConfig {
  if (config.dhcp4) return config;
  const routes: StaticRouteConfig[] = [];
  for (const route of config.staticRoutes) {
    if (isDefaultRoute(route.to) && !config.gateway) {
      config.gateway = route.via;
      if (route.metric > 0) config.metric = route.metric;
    } else {
      routes.push(route);
    }
  }
  return { ...config, staticRoutes: routes };
}

function generateNetplanConfig(configs: InterfaceConfig[]) {
  const lines = ["# /etc/netplan/99-custom-config.yaml", "# Managed by drone-management", "network:", "  version: 2", "  renderer: networkd", "  ethernets:"];
  for (const config of configs) {
    lines.push(`    ${config.name.trim()}:`, `      dhcp4: ${config.dhcp4 ? "true" : "false"}`);
    if (config.dhcp4 && config.metric > 0) lines.push("      dhcp4-overrides:", `        route-metric: ${config.metric}`);
    if (!config.dhcp4 && config.addresses.some((item) => item.ip.trim())) {
      lines.push("      addresses:");
      config.addresses.filter((item) => item.ip.trim()).forEach((item) => lines.push(`        - ${addressToCIDR(item)}`));
    }
    const routeEntries = [...config.staticRoutes.filter((item) => item.to.trim() && item.via.trim())];
    if (!config.dhcp4 && config.gateway.trim()) routeEntries.unshift({ to: "default", via: config.gateway.trim(), metric: config.metric });
    if (routeEntries.length) {
      lines.push("      routes:");
      routeEntries.forEach((route) => {
        lines.push(`        - to: ${route.to.trim()}`, `          via: ${route.via.trim()}`);
        if (route.metric > 0) lines.push(`          metric: ${route.metric}`);
      });
    }
    const dns = config.dns.map((item) => item.trim()).filter(Boolean);
    if (dns.length) {
      lines.push("      nameservers:", "        addresses:");
      dns.forEach((item) => lines.push(`          - ${item}`));
    }
    lines.push(`      optional: ${config.optional ? "true" : "false"}`);
  }
  return `${lines.join("\n")}\n`;
}

function applyRouteField(route: StaticRouteConfig, line: string) {
  const index = line.indexOf(":");
  if (index < 0) return false;
  const key = line.slice(0, index).trim();
  const value = stripYAMLValue(line.slice(index + 1));
  if (key === "to") route.to = value;
  else if (key === "via") route.via = value;
  else if (key === "metric") {
    const metric = Number(value);
    if (!Number.isInteger(metric) || metric < 0) return false;
    route.metric = metric;
  } else return false;
  return true;
}

function stripYAMLValue(value: string) {
  return value.trim().replace(/^['"]|['"]$/g, "");
}

function isDefaultRoute(value: string) {
  return ["default", "0.0.0.0/0", "::/0"].includes(value.trim().toLowerCase());
}

function parseIPv4CIDR(value: string): AddressConfig | null {
  const [ip = "", prefix = "", ...rest] = value.split("/");
  const prefixNumber = Number(prefix);
  if (rest.length || !isIPv4Address(ip) || !Number.isInteger(prefixNumber) || prefixNumber < 0 || prefixNumber > 32) return null;
  return { ip, netmask: cidrToNetmask(prefixNumber) };
}

function isIPv4Address(value: string) {
  const parts = value.trim().split(".");
  return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) >= 0 && Number(part) <= 255);
}

function isIPv4RouteDestination(value: string) {
  const normalized = value.trim().toLowerCase();
  return normalized === "default" || parseIPv4CIDR(normalized) !== null;
}

function addressToCIDR(value: AddressConfig) {
  return `${value.ip.trim()}/${netmaskToCIDR(value.netmask)}`;
}

function cidrToNetmask(prefix: number) {
  return [0, 1, 2, 3].map((index) => {
    const bits = Math.max(0, Math.min(8, prefix - index * 8));
    return bits === 0 ? 0 : 256 - 2 ** (8 - bits);
  }).join(".");
}

function netmaskToCIDR(mask: string) {
  const binary = mask.split(".").map((part) => Number(part).toString(2).padStart(8, "0")).join("");
  return /^1*0*$/.test(binary) ? binary.indexOf("0") === -1 ? 32 : binary.indexOf("0") : 24;
}

function formatConflict(value: string) {
  return value.replace(/^service_state:/, "Service: ").replaceAll("_", " ");
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`;
  return `${(value / 1024).toFixed(1)} KB`;
}
