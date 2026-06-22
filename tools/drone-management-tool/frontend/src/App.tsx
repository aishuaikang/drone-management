import { useCallback, useEffect, useRef, useState } from "react";
import {
  Activity,
  ArrowUp,
  Check,
  CheckCircle2,
  FolderOpen,
  HardDriveUpload,
  Info,
  PlugZap,
  Power,
  RefreshCw,
  Server,
  ShieldAlert,
  TerminalSquare,
  UploadCloud,
  XCircle,
} from "lucide-react";

import type { ProgressEvent, RemoteEntry, RemoteProbe, SavedConfig, SSHStatus } from "./types";
import { api, onProgress } from "./wails";

type Notice = { tone: "idle" | "success" | "error" | "loading"; message: string };
type SSHForm = { host: string; port: number; user: string; password: string; rememberPassword: boolean };

const defaultInstallDir = "/spbatc/drone-management";
const installDirName = "drone-management";

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

function appendProgress(setter: (fn: (items: ProgressEvent[]) => ProgressEvent[]) => void, event: ProgressEvent) {
  setter((items) => {
    const nextItems = items.map((item) => {
      if (item.step < event.step && item.status === "running") {
        return { ...item, status: "success", progress: 100 };
      }
      return item;
    });
    const index = nextItems.findIndex((item) => item.step === event.step);
    if (index === -1) {
      return [...nextItems, event].slice(-10);
    }
    const next = [...nextItems];
    next[index] = event;
    return next;
  });
}

function statusTone(value: boolean) {
  return value ? "ok" : "warn";
}

function serviceStatusLabel(value: string) {
  switch (value) {
    case "active":
      return "运行中";
    case "activating":
      return "启动中";
    case "inactive":
      return "未运行";
    case "failed":
      return "失败";
    case "deactivating":
      return "停止中";
    default:
      return value || "-";
  }
}

function enabledStatusLabel(value: boolean) {
  return value ? "已启用" : "未启用";
}

function healthStatusLabel(value: string, ok: boolean) {
  if (ok) {
    return "正常";
  }
  switch (value) {
    case "no":
      return "未通过";
    case "unknown":
      return "未知";
    default:
      return value || "-";
  }
}

function parentRemoteDir(path: string) {
  const clean = path.trim().replace(/\/+$/, "") || "/";
  if (clean === "/") {
    return "/";
  }
  const index = clean.lastIndexOf("/");
  return index <= 0 ? "/" : clean.slice(0, index);
}

function normalizeRemoteDir(path: string) {
  const clean = path.trim().replace(/\/+$/, "");
  return clean || "/";
}

function installDirFromParent(path: string) {
  const parent = normalizeRemoteDir(path);
  if (parent === "/") {
    return `/${installDirName}`;
  }
  if (parent.split("/").at(-1) === installDirName) {
    return parent;
  }
  return `${parent}/${installDirName}`;
}

function pickerStartDir(path: string) {
  const clean = normalizeRemoteDir(path);
  if (clean.split("/").at(-1) === installDirName) {
    return parentRemoteDir(clean);
  }
  return clean;
}

export default function App() {
  const [config, setConfig] = useState<SavedConfig>({ installDir: defaultInstallDir });
  const [ssh, setSSH] = useState<SSHForm>({ host: "", port: 22, user: "root", password: "", rememberPassword: false });
  const [sshStatus, setSSHStatus] = useState<SSHStatus>({ connected: false, message: "未连接" });
  const sshStatusRef = useRef<SSHStatus>(sshStatus);
  const [entered, setEntered] = useState(false);
  const [installDir, setInstallDir] = useState(defaultInstallDir);
  const [releasePackage, setReleasePackage] = useState("");
  const [probe, setProbe] = useState<RemoteProbe | null>(null);
  const [dirPicker, setDirPicker] = useState<{ open: boolean; path: string; entries: RemoteEntry[]; loading: boolean }>({
    open: false,
    path: "/",
    entries: [],
    loading: false,
  });
  const [notice, setNotice] = useState<Notice>({ tone: "idle", message: "" });
  const [deployProgress, setDeployProgress] = useState<ProgressEvent[]>([]);
  const [busy, setBusy] = useState("");

  const updateSSHStatus = useCallback((status: SSHStatus) => {
    sshStatusRef.current = status;
    setSSHStatus(status);
  }, []);

  useEffect(() => {
    return onProgress("deploy-progress", (event) => appendProgress(setDeployProgress, event));
  }, []);

  useEffect(() => {
    void (async () => {
      try {
        const [loaded, status] = await Promise.all([api.loadConfig(), api.getSSHStatus()]);
        const loadedInstallDir = loaded.installDir || defaultInstallDir;
        setConfig(loaded);
        setInstallDir(loadedInstallDir);
        setReleasePackage(loaded.releasePackage || "");
        setSSH((current) => ({
          ...current,
          host: loaded.ssh?.host || current.host,
          port: loaded.ssh?.port || current.port,
          user: loaded.ssh?.user || current.user,
          password: loaded.ssh?.password || "",
          rememberPassword: Boolean(loaded.ssh?.rememberPassword),
        }));
        updateSSHStatus(status);
        if (status.connected) {
          setEntered(true);
          void runProbe(loadedInstallDir, true);
        }
      } catch (error) {
        setNotice({ tone: "error", message: messageOf(error) });
      }
    })();
  }, [updateSSHStatus]);

  const persistConfig = useCallback((patch: Partial<SavedConfig>) => {
    setConfig((current) => {
      const next = { ...current, ...patch };
      void api.saveConfig(next).catch(() => undefined);
      return next;
    });
  }, []);

  const connected = sshStatus.connected;

  const connect = async () => {
    setBusy("ssh");
    setNotice({ tone: "loading", message: "正在连接 SSH" });
    try {
      const status = await api.connectSSH(ssh);
      updateSSHStatus(status);
      setEntered(true);
      setConfig((current) => ({
        ...current,
        ssh: {
          host: status.host || ssh.host,
          port: status.port || ssh.port,
          user: status.user || ssh.user,
          rememberPassword: ssh.rememberPassword,
          password: ssh.rememberPassword ? ssh.password : "",
        },
      }));
      setProbe(null);
      setDeployProgress([]);
      setNotice({ tone: "success", message: status.message });
      await runProbe(installDir, true);
    } catch (error) {
      setEntered(false);
      setNotice({ tone: "error", message: messageOf(error) });
    } finally {
      setBusy("");
    }
  };

  const reconnect = async () => {
    setBusy("reconnect");
    setNotice({ tone: "loading", message: "正在重连设备" });
    try {
      const status = await api.reconnectSSH();
      updateSSHStatus(status);
      setEntered(true);
      setProbe(null);
      setNotice({ tone: "success", message: status.message || "设备已重连" });
      await runProbe(installDir, true);
    } catch (error) {
      const message = messageOf(error);
      updateSSHStatus({ ...sshStatusRef.current, connected: false, message });
      if (message.includes("密码")) {
        setEntered(false);
      }
      setNotice({ tone: "error", message });
    } finally {
      setBusy("");
    }
  };

  const disconnect = async () => {
    setBusy("disconnect");
    try {
      await api.disconnectSSH();
    } finally {
      updateSSHStatus({ connected: false, message: "未连接" });
      setEntered(false);
      setBusy("");
      setProbe(null);
      setNotice({ tone: "idle", message: "SSH 已断开" });
    }
  };

  const runProbe = async (targetInstallDir = installDir, auto = false) => {
    setBusy("probe");
    setNotice({ tone: "loading", message: auto ? "正在自动探测设备" : "正在探测设备" });
    try {
      const result = await api.probeRemote(targetInstallDir);
      setProbe(result);
      persistConfig({ installDir: targetInstallDir });
      setNotice({
        tone: result.warnings?.length ? "error" : "success",
        message: result.warnings?.join("；") || (auto ? "自动探测完成" : "探测完成"),
      });
    } catch (error) {
      setNotice({ tone: "error", message: messageOf(error) });
    } finally {
      setBusy("");
    }
  };

  const chooseReleasePackage = async () => {
    try {
      const path = await api.selectReleasePackage();
      if (!path) {
        return;
      }
      setReleasePackage(path);
      persistConfig({ releasePackage: path });
    } catch (error) {
      setNotice({ tone: "error", message: messageOf(error) });
    }
  };

  const browseRemoteDir = async (path: string) => {
    const targetPath = path.trim() || "/";
    setDirPicker((current) => ({ ...current, path: targetPath, loading: true }));
    try {
      const entries = await api.browseRemoteDir(targetPath);
      setDirPicker({
        open: true,
        path: targetPath,
        entries: entries.filter((entry) => entry.isDir),
        loading: false,
      });
    } catch (error) {
      setDirPicker((current) => ({ ...current, loading: false }));
      setNotice({ tone: "error", message: messageOf(error) });
    }
  };

  const openInstallDirPicker = () => {
    if (!connected) {
      setNotice({ tone: "error", message: "请先连接设备" });
      return;
    }
    const targetPath = pickerStartDir(installDir);
    setDirPicker({ open: true, path: targetPath, entries: [], loading: true });
    void browseRemoteDir(targetPath);
  };

  const chooseInstallDir = (path: string) => {
    const nextInstallDir = installDirFromParent(path);
    setInstallDir(nextInstallDir);
    persistConfig({ installDir: nextInstallDir });
    setDirPicker((current) => ({ ...current, open: false }));
  };

  const deploy = async () => {
    setBusy("deploy");
    setDeployProgress([]);
    setNotice({ tone: "loading", message: "正在部署 Drone Management" });
    try {
      const result = await api.deployDroneManagement({ installDir, releasePackagePath: releasePackage });
      setNotice({ tone: "success", message: result.message });
      await runProbe(result.installDir);
    } catch (error) {
      setNotice({ tone: "error", message: messageOf(error) });
    } finally {
      setBusy("");
    }
  };

  if (!entered) {
    return (
      <main className="login-shell">
        <section className="login-panel">
          <div className="brand-mark">
            <HardDriveUpload size={32} />
            <div>
              <h1>Drone Management Tool</h1>
              <p>设备部署工作台</p>
            </div>
          </div>
          <div className="login-grid">
            <label>
              主机
              <input value={ssh.host} onChange={(event) => setSSH({ ...ssh, host: event.target.value })} placeholder="192.168.100.10" />
            </label>
            <label>
              端口
              <input
                min={1}
                max={65535}
                type="number"
                value={ssh.port}
                onChange={(event) => setSSH({ ...ssh, port: Number(event.target.value) })}
              />
            </label>
            <label>
              用户
              <input value={ssh.user} onChange={(event) => setSSH({ ...ssh, user: event.target.value })} />
            </label>
            <label>
              密码
              <input type="password" value={ssh.password} onChange={(event) => setSSH({ ...ssh, password: event.target.value })} />
            </label>
            <label className="checkbox wide">
              <input
                checked={ssh.rememberPassword}
                type="checkbox"
                onChange={(event) => setSSH({ ...ssh, rememberPassword: event.target.checked })}
              />
              记住密码
            </label>
          </div>
          <div className="actions">
            <button className="primary" type="button" disabled={busy === "ssh"} onClick={() => void connect()}>
              <PlugZap size={16} />
              {busy === "ssh" ? "连接中" : "连接设备"}
            </button>
          </div>
          <NoticeBar notice={notice} />
        </section>
      </main>
    );
  }

  return (
    <main className="app-shell">
      <header className="topbar">
        <div className="title-block">
          <span className="eyebrow">Drone Management Tool</span>
          <h1>部署控制台</h1>
        </div>
        <div className="connection">
          <span className={`connection-pill ${connected ? "ok" : "warn"}`}>
            <span className="dot" />
            <span>{connected ? `${sshStatus.user}@${sshStatus.host}:${sshStatus.port}` : sshStatus.message}</span>
          </span>
          <button type="button" disabled={busy === "reconnect"} onClick={() => void reconnect()}>
            <RefreshCw size={16} />
            重连
          </button>
          <button type="button" disabled={busy === "disconnect"} onClick={() => void disconnect()}>
            <Power size={16} />
            断开
          </button>
        </div>
      </header>

      <section className="workspace">
        <section className="panel deploy-panel">
          <div className="panel-title">
            <div>
              <TerminalSquare size={18} />
              <h2>部署参数</h2>
            </div>
            <span>install</span>
          </div>
          <div className="form-grid">
            <label className="file-field wide">
              安装目录
              <div>
                <input
                  value={installDir}
                  onChange={(event) => {
                    setInstallDir(event.target.value);
                    persistConfig({ installDir: event.target.value });
                  }}
                />
                <button type="button" disabled={!connected} onClick={openInstallDirPicker}>
                  <FolderOpen size={16} />
                  选择
                </button>
              </div>
            </label>
            <label className="file-field wide">
              发布包
              <div>
                <input readOnly value={releasePackage} placeholder="选择 Linux .tar.gz 发布包" />
                <button type="button" onClick={() => void chooseReleasePackage()}>
                  <FolderOpen size={16} />
                  选择
                </button>
              </div>
            </label>
          </div>
          <div className="actions">
            <button type="button" disabled={!connected || busy === "probe"} onClick={() => void runProbe()}>
              <RefreshCw size={16} />
              探测
            </button>
            <button
              className="primary"
              type="button"
              disabled={!connected || !releasePackage || busy === "deploy"}
              onClick={() => void deploy()}
            >
              <UploadCloud size={16} />
              {busy === "deploy" ? "部署中" : "开始烧录"}
            </button>
          </div>
          <NoticeBar notice={notice} />
        </section>

        <section className="panel status-panel">
          <div className="panel-title">
            <div>
              <Server size={18} />
              <h2>设备状态</h2>
            </div>
            <span>remote</span>
          </div>
          {probe ? (
            <div className="status-grid">
              <StatusTile label="服务运行" value={serviceStatusLabel(probe.serviceStatus)} tone={statusTone(probe.serviceActive)} />
              <StatusTile label="开机自启" value={enabledStatusLabel(probe.serviceEnabled)} tone={statusTone(probe.serviceEnabled)} />
              <StatusTile label="健康检查" value={healthStatusLabel(probe.healthStatus, probe.healthOk)} tone={statusTone(probe.healthOk)} />
              <StatusTile label="systemd" value={probe.hasSystemd ? "可用" : "不可用"} tone={statusTone(probe.hasSystemd)} />
              <StatusTile label="tar" value={probe.hasTar ? "可用" : "不可用"} tone={statusTone(probe.hasTar)} />
              <StatusTile label="二进制" value={probe.binaryExists ? "已安装" : "未找到"} tone={statusTone(probe.binaryExists)} />
            </div>
          ) : (
            <div className="empty-state">
              <ShieldAlert size={22} />
              <span>尚未探测</span>
            </div>
          )}
        </section>

        <section className="panel progress-panel">
          <div className="panel-title">
            <div>
              <HardDriveUpload size={18} />
              <h2>部署进度</h2>
            </div>
            <span>progress</span>
          </div>
          <ProgressList items={deployProgress} />
        </section>
      </section>
      {dirPicker.open ? (
        <RemoteDirPicker
          entries={dirPicker.entries}
          loading={dirPicker.loading}
          onClose={() => setDirPicker((current) => ({ ...current, open: false }))}
          onEnter={(path) => void browseRemoteDir(path)}
          onSelect={chooseInstallDir}
          onUp={() => void browseRemoteDir(parentRemoteDir(dirPicker.path))}
          path={dirPicker.path}
        />
      ) : null}
    </main>
  );
}

function NoticeBar({ notice }: { notice: Notice }) {
  if (!notice.message) {
    return null;
  }
  return (
    <div className={`notice ${notice.tone}`}>
      <Info size={15} />
      <span>{notice.message}</span>
    </div>
  );
}

function StatusTile({ label, value, tone }: { label: string; value: string; tone: "ok" | "warn" }) {
  return (
    <div className={`status-tile ${tone}`}>
      <span className="status-dot">{tone === "ok" ? <Check size={12} /> : <Activity size={12} />}</span>
      <span className="status-label">{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function ProgressList({ items }: { items: ProgressEvent[] }) {
  if (items.length === 0) {
    return <div className="empty-state">暂无进度</div>;
  }
  return (
    <div className="progress-list">
      {items.map((item) => (
        <div className={`progress-row ${item.status}`} key={item.step}>
          <div className="progress-icon">
            {item.status === "success" ? <CheckCircle2 size={16} /> : item.status === "error" ? <XCircle size={16} /> : <RefreshCw size={16} />}
          </div>
          <div className="progress-body">
            <div className="progress-top">
              <strong>{item.stepName}</strong>
              <span>{item.progress}%</span>
            </div>
            <div className="progress-track">
              <span style={{ width: `${item.progress}%` }} />
            </div>
            <p>{item.errorDetail || item.message}</p>
          </div>
        </div>
      ))}
    </div>
  );
}

function RemoteDirPicker({
  entries,
  loading,
  onClose,
  onEnter,
  onSelect,
  onUp,
  path,
}: {
  entries: RemoteEntry[];
  loading: boolean;
  onClose: () => void;
  onEnter: (path: string) => void;
  onSelect: (path: string) => void;
  onUp: () => void;
  path: string;
}) {
  return (
    <div className="modal-scrim" role="presentation" onClick={onClose}>
      <section className="modal" role="dialog" aria-modal="true" aria-labelledby="dir-picker-title" onClick={(event) => event.stopPropagation()}>
        <header>
          <div>
            <h2 id="dir-picker-title">选择安装父目录</h2>
            <p>{path}</p>
          </div>
          <button type="button" onClick={onClose}>
            关闭
          </button>
        </header>
        <div className="dir-actions">
          <button type="button" disabled={loading || path === "/"} onClick={onUp}>
            <ArrowUp size={16} />
            上一级
          </button>
          <button className="primary" type="button" disabled={loading} onClick={() => onSelect(path)}>
            <CheckCircle2 size={16} />
            安装到此目录下
          </button>
        </div>
        <div className="dir-list">
          {loading ? (
            <div className="empty-state compact">正在读取目录</div>
          ) : entries.length === 0 ? (
            <div className="empty-state compact">暂无子目录</div>
          ) : (
            entries.map((entry) => (
              <button className="dir-row" key={entry.path} type="button" onClick={() => onEnter(entry.path)}>
                <FolderOpen size={16} />
                <span>{entry.name}</span>
              </button>
            ))
          )}
        </div>
      </section>
    </div>
  );
}
