export interface SavedConfig {
  ssh?: {
    host: string;
    port: number;
    user: string;
    rememberPassword?: boolean;
    password?: string;
  };
  installDir?: string;
  releasePackage?: string;
}

export interface SSHConnectRequest {
  host: string;
  port: number;
  user: string;
  password: string;
  rememberPassword?: boolean;
}

export interface SSHStatus {
  connected: boolean;
  host?: string;
  port?: number;
  user?: string;
  message: string;
}

export interface NetworkStatus {
  connected: boolean;
  internet: boolean;
  status: string;
  message: string;
}

export interface NetworkPingLog {
  connected: boolean;
  output: string;
  message: string;
}

export interface RemoteProbe {
  installDir: string;
  serviceActive: boolean;
  serviceEnabled: boolean;
  serviceStatus: string;
  hasSystemd: boolean;
  hasTar: boolean;
  binaryExists: boolean;
  healthOk: boolean;
  healthStatus: string;
  warnings?: string[];
}

export interface RemoteEntry {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
}

export interface ProgressEvent {
  step: number;
  stepName: string;
  message: string;
  status: "running" | "success" | "error" | string;
  progress: number;
  errorDetail?: string;
}

export interface DeployRequest {
  installDir: string;
  releasePackagePath: string;
}
