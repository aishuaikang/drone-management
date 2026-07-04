import type {
  EventMessage,
  FPVVideoRecord,
  FPVVideoRecordDeleteRequest,
  FPVVideoRecordDeleteResponse,
  FPVVideoSessionPayload,
  InterferenceReport,
  InterferenceReportDeleteResponse,
  InterferenceReportStatus,
  InterferenceReportSummary,
  IntrusionDeleteRequest,
  IntrusionDeleteResponse,
  IntrusionRecord,
  LicenseInfo,
  LicenseUploadResponse,
  ListResponse,
  OfflineMapStatus,
  OfflineMapUploadLog,
  OfflineMapUploadResponse,
  ScreenDeviceLocationResponse,
  ScreenFPVTarget,
  ScreenTCPPortRequest,
  ScreenManualDeviceLocationRequest,
  ScreenPositionTarget,
  ScreenRuntimeStatus,
  ScreenStrikeRequest,
  ScreenStrikeResponse,
  ScreenStrikeState,
  ScreenStrikeUnattendedConfig,
  UserSettings
} from "./types";

const API_PREFIX = "/api/v1";
export const FPV_VIDEO_SESSION_BUSY_CODE = "busy";

export type FPVVideoSessionError = Error & {
  code?: string;
};

export type OfflineMapUploadError = Error & {
  logs?: OfflineMapUploadLog[];
};

export type LicenseUploadError = Error & {
  code?: string;
  license?: LicenseInfo;
};

type JsonRequestInit = RequestInit & {
  timeoutMs?: number;
};

async function requestJson<T>(path: string, init?: JsonRequestInit): Promise<T> {
  const { timeoutMs = 0, ...requestInit } = init ?? {};
  const headers = new Headers(requestInit.headers);
  headers.set("Accept", "application/json");
  if (requestInit.body && !(requestInit.body instanceof FormData)) {
    headers.set("Content-Type", "application/json");
  }
  const controller = timeoutMs > 0 && !requestInit.signal ? new AbortController() : null;
  const timeout = controller ? window.setTimeout(() => controller.abort(), timeoutMs) : 0;
  try {
    const response = await fetch(`${API_PREFIX}${path}`, {
      ...requestInit,
      headers,
      signal: requestInit.signal ?? controller?.signal,
    });
    if (!response.ok) {
      throw new Error(await responseErrorMessage(response));
    }
    return (await response.json()) as T;
  } finally {
    if (timeout) {
      window.clearTimeout(timeout);
    }
  }
}

async function requestBlob(path: string, init?: RequestInit): Promise<{ blob: Blob; fileName: string }> {
  const headers = new Headers(init?.headers);
  headers.set("Accept", "application/octet-stream");
  if (init?.body) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(`${API_PREFIX}${path}`, {
    ...init,
    headers,
  });
  if (!response.ok) {
    throw new Error(await responseErrorMessage(response));
  }
  return {
    blob: await response.blob(),
    fileName: responseFileName(response.headers.get("Content-Disposition")),
  };
}

async function responseErrorMessage(response: Response) {
  const fallback = `请求失败: ${response.status}`;
  const text = await response.text().catch(() => "");
  if (!text.trim()) {
    return fallback;
  }
  try {
    const parsed = JSON.parse(text) as unknown;
    if (parsed && typeof parsed === "object" && "message" in parsed) {
      const message = (parsed as { message?: unknown }).message;
      if (typeof message === "string" && message.trim()) {
        return message.trim();
      }
    }
  } catch {
    return text.trim();
  }
  return fallback;
}

function responseFileName(contentDisposition: string | null) {
  if (!contentDisposition) {
    return "";
  }
  const utf8Match = contentDisposition.match(/filename\*=UTF-8''([^;]+)/i);
  if (utf8Match?.[1]) {
    try {
      return decodeURIComponent(utf8Match[1].trim());
    } catch {
      return utf8Match[1].trim();
    }
  }
  const asciiMatch = contentDisposition.match(/filename="?([^";]+)"?/i);
  return asciiMatch?.[1]?.trim() ?? "";
}

export function getScreenStatus() {
  return requestJson<ScreenRuntimeStatus>("/screen/status", { timeoutMs: 2500 });
}

export function getScreenPositions(limit = 100) {
  return requestJson<ListResponse<ScreenPositionTarget>>(`/screen/positions?limit=${limit}`);
}

export function getScreenFPV(limit = 100) {
  return requestJson<ListResponse<ScreenFPVTarget>>(`/screen/fpv?limit=${limit}`);
}

export function getScreenDeviceLocation() {
  return requestJson<ScreenDeviceLocationResponse>("/screen/device-location");
}

export function getScreenStrike() {
  return requestJson<ScreenStrikeState>("/screen/strike");
}

export function updateScreenTCPPorts(payload: ScreenTCPPortRequest) {
  return requestJson<ScreenRuntimeStatus>("/screen/tcp-ports", {
    method: "PUT",
    body: JSON.stringify(payload),
  });
}

export function updateScreenStrike(payload: ScreenStrikeRequest) {
  return requestJson<ScreenStrikeResponse>("/screen/strike", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function updateScreenStrikeUnattended(payload: ScreenStrikeUnattendedConfig) {
  return requestJson<ScreenStrikeResponse>("/screen/strike/unattended", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function setManualDeviceLocation(payload: ScreenManualDeviceLocationRequest) {
  return requestJson<ScreenDeviceLocationResponse>("/screen/device-location/manual", {
    method: "PUT",
    body: JSON.stringify(payload),
  });
}

export function clearManualDeviceLocation() {
  return requestJson<ScreenDeviceLocationResponse>("/screen/device-location/manual", {
    method: "DELETE",
  });
}

export function getOfflineMapStatus() {
  return requestJson<OfflineMapStatus>("/offline-map/status");
}

export function getLicenseStatus() {
  return requestJson<LicenseInfo>("/license/status");
}

export function uploadLicense(file: File) {
  const form = new FormData();
  form.set("file", file);
  return requestLicenseUpload(form);
}

async function requestLicenseUpload(form: FormData): Promise<LicenseUploadResponse> {
  const response = await fetch(`${API_PREFIX}/license/upload`, {
    method: "POST",
    headers: {
      "Accept": "application/json",
    },
    body: form,
  });
  const text = await response.text();
  const payload = parseJSONPayload(text);
  if (!response.ok) {
    const error = new Error(readPayloadMessage(payload, `请求失败: ${response.status}`)) as LicenseUploadError;
    error.code = readPayloadCode(payload);
    error.license = readPayloadLicense(payload);
    throw error;
  }
  return (payload ?? {}) as LicenseUploadResponse;
}

export function uploadOfflineMap(
  file: File,
  keepBackup: boolean,
  onProgress?: (progress: { loaded: number; total: number; percent: number }) => void,
) {
  const form = new FormData();
  form.set("file", file);
  form.set("keepBackup", keepBackup ? "true" : "false");
  return requestOfflineMapUpload(form, onProgress);
}

function requestOfflineMapUpload(
  form: FormData,
  onProgress?: (progress: { loaded: number; total: number; percent: number }) => void,
): Promise<OfflineMapUploadResponse> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${API_PREFIX}/offline-map/upload`);
    xhr.setRequestHeader("Accept", "application/json");
    xhr.upload.onprogress = (event) => {
      if (!event.lengthComputable || !onProgress) {
        return;
      }
      onProgress({
        loaded: event.loaded,
        total: event.total,
        percent: Math.round((event.loaded / event.total) * 100),
      });
    };
    xhr.onload = () => {
      const payload = parseOfflineMapUploadPayload(xhr.responseText);
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve((payload ?? {}) as OfflineMapUploadResponse);
        return;
      }
      const error = new Error(readOfflineMapUploadMessage(payload, `请求失败: ${xhr.status}`)) as OfflineMapUploadError;
      error.logs = readOfflineMapUploadLogs(payload);
      reject(error);
    };
    xhr.onerror = () => {
      reject(new Error("网络请求失败"));
    };
    xhr.onabort = () => {
      reject(new Error("上传已取消"));
    };
    xhr.send(form);
  });
}

function parseOfflineMapUploadPayload(text: string): unknown {
  return parseJSONPayload(text);
}

function parseJSONPayload(text: string): unknown {
  if (!text.trim()) {
    return null;
  }
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return null;
  }
}

function readPayloadMessage(payload: unknown, fallback: string) {
  if (!payload || typeof payload !== "object" || !("message" in payload)) {
    return fallback;
  }
  const message = (payload as { message?: unknown }).message;
  return typeof message === "string" && message.trim() ? message.trim() : fallback;
}

function readPayloadCode(payload: unknown) {
  if (!payload || typeof payload !== "object" || !("code" in payload)) {
    return undefined;
  }
  const code = (payload as { code?: unknown }).code;
  return typeof code === "string" && code.trim() ? code.trim() : undefined;
}

function readPayloadLicense(payload: unknown): LicenseInfo | undefined {
  if (!payload || typeof payload !== "object" || !("details" in payload)) {
    return undefined;
  }
  const details = (payload as { details?: unknown }).details;
  return details && typeof details === "object" ? details as LicenseInfo : undefined;
}

function readOfflineMapUploadMessage(payload: unknown, fallback: string) {
  return readPayloadMessage(payload, fallback);
}

function readOfflineMapUploadLogs(payload: unknown): OfflineMapUploadLog[] {
  if (!payload || typeof payload !== "object") {
    return [];
  }
  const value = "logs" in payload
    ? (payload as { logs?: unknown }).logs
    : "details" in payload
      ? (payload as { details?: unknown }).details
      : null;
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isOfflineMapUploadLog);
}

function isOfflineMapUploadLog(value: unknown): value is OfflineMapUploadLog {
  return Boolean(
    value &&
      typeof value === "object" &&
      typeof (value as { stage?: unknown }).stage === "string" &&
      typeof (value as { message?: unknown }).message === "string" &&
      typeof (value as { status?: unknown }).status === "string" &&
      typeof (value as { timestamp?: unknown }).timestamp === "string",
  );
}

export function getUserSettings() {
  return requestJson<UserSettings>("/user/settings", { timeoutMs: 5000 });
}

export function updateUserSettings(payload: UserSettings) {
  return requestJson<UserSettings>("/user/settings", {
    method: "PUT",
    body: JSON.stringify(payload),
  });
}

export type IntrusionQuery = {
  model?: string;
  serial?: string;
  dateFrom?: string;
  dateTo?: string;
};

export function getIntrusions(limit = 50, offset = 0, query: IntrusionQuery = {}) {
  const params = new URLSearchParams({
    limit: String(limit),
    offset: String(offset),
  });
  if (query.model?.trim()) {
    params.set("model", query.model.trim());
  }
  if (query.serial?.trim()) {
    params.set("serial", query.serial.trim());
  }
  if (query.dateFrom) {
    params.set("dateFrom", query.dateFrom);
  }
  if (query.dateTo) {
    params.set("dateTo", query.dateTo);
  }
  return requestJson<ListResponse<IntrusionRecord>>(`/intrusions?${params.toString()}`);
}

export function deleteIntrusions(payload: IntrusionDeleteRequest) {
  return requestJson<IntrusionDeleteResponse>("/intrusions", {
    method: "DELETE",
    body: JSON.stringify(payload),
  });
}

export type FPVVideoRecordQuery = {
  signalType?: string;
  deviceSn?: string;
  dateFrom?: string;
  dateTo?: string;
};

export function getFPVVideoRecords(limit = 50, offset = 0, query: FPVVideoRecordQuery = {}) {
  const params = new URLSearchParams({
    limit: String(limit),
    offset: String(offset),
  });
  if (query.signalType?.trim()) {
    params.set("signalType", query.signalType.trim());
  }
  if (query.deviceSn?.trim()) {
    params.set("deviceSn", query.deviceSn.trim());
  }
  if (query.dateFrom) {
    params.set("dateFrom", query.dateFrom);
  }
  if (query.dateTo) {
    params.set("dateTo", query.dateTo);
  }
  return requestJson<ListResponse<FPVVideoRecord>>(`/fpv-video-records?${params.toString()}`);
}

export function deleteFPVVideoRecords(payload: FPVVideoRecordDeleteRequest) {
  return requestJson<FPVVideoRecordDeleteResponse>("/fpv-video-records", {
    method: "DELETE",
    body: JSON.stringify(payload),
  });
}

export function exportFPVVideoRecords(payload: FPVVideoRecordDeleteRequest) {
  return requestBlob("/fpv-video-records/export", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export type InterferenceReportQuery = {
  status?: InterferenceReportStatus | "all";
};

export function getInterferenceReports(limit = 50, offset = 0, query: InterferenceReportQuery = {}) {
  const params = new URLSearchParams({
    limit: String(limit),
    offset: String(offset),
  });
  if (query.status && query.status !== "all") {
    params.set("status", query.status);
  }
  return requestJson<ListResponse<InterferenceReportSummary>>(`/interference-reports?${params.toString()}`);
}

export function getInterferenceReport(id: string) {
  return requestJson<InterferenceReport>(`/interference-reports/${encodeURIComponent(id)}`);
}

export function deleteFailedInterferenceReport(id: string) {
  return requestJson<InterferenceReportDeleteResponse>(`/interference-reports/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export function openFPVVideoSession(frequency: number, targetId: string, handlers: {
  onReady: (event: EventMessage<FPVVideoSessionPayload>) => void;
  onError: (error: FPVVideoSessionError) => void;
  onDisconnect?: () => void;
}) {
  const params = new URLSearchParams({ frequency: String(Math.round(frequency)) });
  if (targetId) {
    params.set("targetId", targetId);
  }
  const source = new EventSource(`${API_PREFIX}/screen/fpv-video/session?${params.toString()}`);
  let closed = false;
  let sessionToken = "";

  const close = (notifyBackend = true) => {
    const token = sessionToken;
    closed = true;
    if (notifyBackend && token) {
      closeFPVVideoSessionEventually(token);
    }
    source.close();
  };
  const parseEvent = (message: MessageEvent<string>) => {
    return JSON.parse(message.data) as EventMessage<FPVVideoSessionPayload>;
  };

  source.addEventListener("screen.fpv_video.ready", (message) => {
    if (closed) {
      return;
    }
    try {
      const event = parseEvent(message as MessageEvent<string>);
      sessionToken = event.payload?.session ?? "";
      handlers.onReady(event);
    } catch (error) {
      handlers.onError(error instanceof Error ? error : new Error(String(error)));
      close();
    }
  });
  source.addEventListener("screen.fpv_video.error", (message) => {
    try {
      const event = parseEvent(message as MessageEvent<string>);
      const error = new Error(event.payload?.message || "FPV video session failed") as FPVVideoSessionError;
      error.code = event.payload?.code;
      handlers.onError(error);
    } catch (error) {
      handlers.onError(error instanceof Error ? error : new Error(String(error)));
    } finally {
      close();
    }
  });
  source.onerror = () => {
    if (closed) {
      return;
    }
    closed = true;
    source.close();
    handlers.onDisconnect?.();
  };

  return close;
}

export async function closeFPVVideoSession(sessionToken: string) {
  const params = new URLSearchParams({ session: sessionToken });
  const url = `${API_PREFIX}/screen/fpv-video/session/close?${params.toString()}`;
  const response = await fetch(url, { method: "POST" });
  if (!response.ok) {
    const message = await readErrorMessage(response);
    throw new Error(message || `请求失败: ${response.status}`);
  }
}

export function closeFPVVideoSessionEventually(sessionToken: string) {
  const params = new URLSearchParams({ session: sessionToken });
  const url = `${API_PREFIX}/screen/fpv-video/session/close?${params.toString()}`;
  if (typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function") {
    if (navigator.sendBeacon(url)) {
      return;
    }
  }
  void fetch(url, {
    method: "POST",
    keepalive: true,
  }).catch(() => undefined);
}

async function readErrorMessage(response: Response) {
  try {
    const body = await response.json() as { message?: string };
    return body.message ?? "";
  } catch {
    return "";
  }
}

export function openScreenStream(handlers: {
  onPosition?: (event: EventMessage<ScreenPositionTarget>) => void;
  onPositionRemoved?: (event: EventMessage<ScreenPositionTarget>) => void;
  onFPV?: (event: EventMessage<ScreenFPVTarget>) => void;
  onDeviceLocation?: (event: EventMessage<ScreenDeviceLocationResponse>) => void;
  onStrike?: (event: EventMessage<ScreenStrikeState>) => void;
  onError?: (error: Error) => void;
}) {
  const source = new EventSource(`${API_PREFIX}/screen/stream`);

  const bind = <T,>(eventName: string, handler?: (event: EventMessage<T>) => void) => {
    if (!handler) {
      return;
    }
    source.addEventListener(eventName, (message) => {
      try {
        handler(JSON.parse((message as MessageEvent<string>).data) as EventMessage<T>);
      } catch (error) {
        handlers.onError?.(error instanceof Error ? error : new Error(String(error)));
      }
    });
  };

  bind("screen.position.updated", handlers.onPosition);
  bind("screen.position.removed", handlers.onPositionRemoved);
  bind("screen.fpv.updated", handlers.onFPV);
  bind("screen.device_location.updated", handlers.onDeviceLocation);
  bind("screen.strike.updated", handlers.onStrike);
  source.onerror = () => {
    if (source.readyState === EventSource.CLOSED) {
      handlers.onError?.(new Error("实时连接已断开"));
    }
  };

  return () => source.close();
}
