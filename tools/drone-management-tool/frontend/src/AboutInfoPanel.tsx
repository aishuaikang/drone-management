import { useEffect, useState } from "react";
import { Check, FolderOpen, Info, RefreshCw } from "lucide-react";

import type { AboutInfo } from "./types";
import { api } from "./wails";

export function AboutInfoPanel({
  installDir = "",
  local = false,
  connected = false,
  disabled = false,
}: {
  installDir?: string;
  local?: boolean;
  connected?: boolean;
  disabled?: boolean;
}) {
  const [localDir, setLocalDir] = useState("");
  const [info, setInfo] = useState<AboutInfo>({ userCompany: "", userName: "" });
  const [loadedDir, setLoadedDir] = useState("");
  const [busy, setBusy] = useState("");
  const [message, setMessage] = useState("");
  const [failed, setFailed] = useState(false);
  const targetDir = local ? localDir : installDir;
  const accessible = (local || connected) && Boolean(targetDir.trim()) && !busy && !disabled;
  const editable = accessible && loadedDir === targetDir;

  useEffect(() => {
    setLoadedDir("");
    setInfo({ userCompany: "", userName: "" });
    setMessage("");
  }, [targetDir, connected]);

  const readInfo = async () => {
    if (!accessible) return;
    setBusy("read");
    setMessage("");
    try {
      setInfo(await api.readAboutInfo(targetDir, local));
      setLoadedDir(targetDir);
      setFailed(false);
      setMessage("当前软件信息已读取");
    } catch (error) {
      setLoadedDir("");
      setFailed(true);
      setMessage(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy("");
    }
  };

  const chooseDir = async () => {
    setBusy("choose");
    try {
      const path = await api.selectLocalInstallDir();
      if (path) setLocalDir(path);
    } catch (error) {
      setFailed(true);
      setMessage(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy("");
    }
  };

  const saveInfo = async () => {
    if (!editable) return;
    setBusy("save");
    setMessage("");
    try {
      setInfo(await api.saveAboutInfo({ installDir: targetDir, local, info }));
      setFailed(false);
      setMessage("信息已保存，刷新主程序“关于”页面后生效");
    } catch (error) {
      setFailed(true);
      setMessage(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy("");
    }
  };

  return (
    <section className="panel about-info-panel">
      <div className="panel-title">
        <div><Info size={18} /><h2>软件信息</h2></div>
        <span>{local ? "本机" : "SSH 设备"}</span>
      </div>
      <p className="about-info-hint">先读取当前信息，再编辑使用厂家和使用人员。主程序“关于”页面只读展示。</p>
      <form onSubmit={(event) => { event.preventDefault(); void saveInfo(); }}>
        <div className="about-info-grid">
          {local ? (
            <label className="file-field wide">
              程序目录
              <div>
                <input readOnly value={localDir} placeholder="选择 Drone Management 程序所在目录" />
                <button type="button" disabled={Boolean(busy)} onClick={() => void chooseDir()}><FolderOpen size={16} />选择</button>
              </div>
            </label>
          ) : <p className="about-info-hint wide">安装目录：{installDir}</p>}
          <label className="wide">生产厂家<input readOnly value="深圳市特信电子有限公司" /></label>
          <label>使用厂家<input value={info.userCompany} maxLength={128} disabled={!editable} onChange={(event) => { setInfo((current) => ({ ...current, userCompany: event.target.value })); setMessage(""); }} /></label>
          <label>使用人员<input value={info.userName} maxLength={64} disabled={!editable} onChange={(event) => { setInfo((current) => ({ ...current, userName: event.target.value })); setMessage(""); }} /></label>
        </div>
        <div className="actions">
          <button type="button" disabled={!accessible} onClick={() => void readInfo()}><RefreshCw size={16} className={busy === "read" ? "spin-icon" : undefined} />{busy === "read" ? "读取中" : "读取当前信息"}</button>
          <button type="submit" className="primary" disabled={!editable}><Check size={16} />{busy === "save" ? "保存中" : "保存信息"}</button>
        </div>
      </form>
      {message ? <p className={`about-info-notice ${failed ? "error" : "success"}`} role={failed ? "alert" : "status"}>{message}</p> : null}
    </section>
  );
}

export function LocalAboutInfoDialog({ onClose }: { onClose: () => void }) {
  useEffect(() => {
    const handleKey = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [onClose]);

  return (
    <div className="modal-scrim" onClick={onClose}>
      <section className="modal about-info-modal" role="dialog" aria-modal="true" aria-labelledby="local-about-info-title" onClick={(event) => event.stopPropagation()}>
        <header><div><h2 id="local-about-info-title">本机软件信息</h2><p>适用于本机运行的 Windows 版程序。</p></div><button type="button" onClick={onClose}>关闭</button></header>
        <AboutInfoPanel local />
      </section>
    </div>
  );
}
