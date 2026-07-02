import type {
  DeployRequest,
  NetworkPingLog,
  NetworkStatus,
  ProgressEvent,
  RemoteEntry,
  RemoteProbe,
  SavedConfig,
  SSHConnectRequest,
  SSHStatus,
} from "./types";

type AppBridge = {
  LoadConfig(): Promise<SavedConfig>;
  SaveConfig(config: SavedConfig): Promise<void>;
  ConnectSSH(request: SSHConnectRequest): Promise<SSHStatus>;
  ReconnectSSH(): Promise<SSHStatus>;
  DisconnectSSH(): Promise<void>;
  GetSSHStatus(): Promise<SSHStatus>;
  GetNetworkStatus(): Promise<NetworkStatus>;
  GetNetworkPingLog(): Promise<NetworkPingLog>;
  ProbeRemote(installDir: string): Promise<RemoteProbe>;
  BrowseRemoteDir(path: string): Promise<RemoteEntry[]>;
  SelectReleasePackage(): Promise<string>;
  DeployDroneManagement(request: DeployRequest): Promise<{ installDir: string; message: string }>;
};

declare global {
  interface Window {
    go?: {
      main?: {
        App?: AppBridge;
      };
    };
    runtime?: {
      EventsOn?: (name: string, callback: (data: unknown) => void) => (() => void) | void;
      EventsOff?: (name: string) => void;
    };
  }
}

function bridge(): AppBridge {
  const app = window.go?.main?.App;
  if (!app) {
    throw new Error("Wails 运行时未就绪");
  }
  return app;
}

export const api = {
  loadConfig: () => bridge().LoadConfig(),
  saveConfig: (config: SavedConfig) => bridge().SaveConfig(config),
  connectSSH: (request: SSHConnectRequest) => bridge().ConnectSSH(request),
  reconnectSSH: () => bridge().ReconnectSSH(),
  disconnectSSH: () => bridge().DisconnectSSH(),
  getSSHStatus: () => bridge().GetSSHStatus(),
  getNetworkStatus: () => bridge().GetNetworkStatus(),
  getNetworkPingLog: () => bridge().GetNetworkPingLog(),
  probeRemote: (installDir: string) => bridge().ProbeRemote(installDir),
  browseRemoteDir: (path: string) => bridge().BrowseRemoteDir(path),
  selectReleasePackage: () => bridge().SelectReleasePackage(),
  deployDroneManagement: (request: DeployRequest) => bridge().DeployDroneManagement(request),
};

export function onProgress(name: string, callback: (event: ProgressEvent) => void) {
  const off = window.runtime?.EventsOn?.(name, (data) => callback(data as ProgressEvent));
  if (typeof off === "function") {
    return off;
  }
  return () => window.runtime?.EventsOff?.(name);
}
