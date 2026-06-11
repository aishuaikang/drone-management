export interface GeoPoint {
  latitude: number;
  longitude: number;
}

export interface LicenseInfo {
  deviceSn?: string;
  customer?: string;
  issuedAt?: string;
  expiresAt?: string;
  isPermanent: boolean;
  remainingDays?: number;
  valid: boolean;
  code?: string;
  message?: string;
}

export interface LicenseUploadResponse {
  license: LicenseInfo;
  message: string;
}

export interface OfflineMapStatus {
  available: boolean;
  tileCount: number;
  uploadedAt?: string;
  sourceFile?: string;
  path?: string;
  message?: string;
}

export interface OfflineMapUploadResponse {
  map: OfflineMapStatus;
  message: string;
}

export interface TCPListenerStatus {
  address: string;
  host: string;
  port: number;
  listening: boolean;
  listenError?: string;
  sourceConnected: boolean;
  clientAddress?: string;
  updatedAt?: string;
}

export interface ScreenRuntimeStatus {
  position: TCPListenerStatus;
  fpv: TCPListenerStatus;
  deviceTargetAddress: string;
  fpvVideo: FPVVideoStatus;
}

export interface ScreenTCPPortRequest {
  positionTCPPort: number;
  fpvTCPPort: number;
}

export interface FPVVideoStatus {
  enabled: boolean;
  playbackUrl?: string;
  playbackType?: "iframe" | "whep" | string;
  active: boolean;
  activeFrequency?: number;
  activeSince?: string;
}

export interface FPVVideoSessionPayload {
  code?: string;
  frequency?: number;
  message?: string;
  session?: string;
}

export interface ScreenDeviceLocationResponse {
  source: "ddsT1" | "manual" | "none" | string;
  point?: GeoPoint;
  updatedAt?: string;
  valid: boolean;
  locked: boolean;
  rfTempC?: number;
  mainTempC?: number;
  lastStatus?: string;
}

export interface ScreenManualDeviceLocationRequest {
  point: GeoPoint;
}

export interface ScreenPositionPoint {
  latitude: number;
  longitude: number;
}

export interface ScreenPositionTrackPoint extends ScreenPositionPoint {
  speed?: number;
  height?: number;
  time: string;
}

export interface ScreenPositionLastRecord {
  type: string;
  receivedAt: string;
  device?: string;
  serial?: string;
  model?: string;
  frequency?: number;
  rssi?: number;
  cracked?: boolean;
  raw?: string;
}

export interface ScreenPositionTarget {
  id: string;
  correlationId?: string;
  serial: string;
  model: string;
  source: string;
  sources?: string[];
  frequency?: number;
  rssi?: number;
  device?: string;
  drone?: ScreenPositionPoint;
  pilot?: ScreenPositionPoint;
  home?: ScreenPositionPoint;
  droneTrajectory?: ScreenPositionTrackPoint[];
  pilotTrajectory?: ScreenPositionTrackPoint[];
  height?: number;
  altitude?: number;
  speed?: number;
  pilotDistanceM?: number;
  droneDistanceM?: number;
  droneDirectionDeg?: number;
  firstSeen: string;
  lastSeen: string;
  hitCount: number;
  cracked?: boolean;
  lastRecord: ScreenPositionLastRecord;
}

export interface UserSettings {
  intrusionRetentionDays?: number;
  screenTitle?: string;
  positionExpireSeconds?: number;
  positionTCPPort?: number;
  fpvTCPPort?: number;
  screenStrikeChannelLabels?: string[];
  warningZoneEnabled?: boolean;
  warningZoneRadiusMeters?: number;
  warningZones?: WarningZone[];
  whitelist?: WhitelistItem[];
}

export interface WarningZone {
  id: string;
  center: GeoPoint;
  radiusMeters: number;
  createdAt?: string;
}

export interface WhitelistItem {
  serial: string;
  model?: string;
  source?: string;
  createdAt?: string;
}

export type IntrusionTargetType = "position";

export interface IntrusionRecord {
  id: string;
  targetId: string;
  targetType: IntrusionTargetType;
  model?: string;
  displayModel?: string;
  serial?: string;
  device?: string;
  frequency?: number;
  rssi?: number;
  firstSeen: string;
  lastSeen: string;
  durationSeconds: number;
  hitCount: number;
  source?: string;
  sources?: string[];
  cracked?: boolean;
  deviceLocation?: ScreenDeviceLocationResponse;
  drone?: ScreenPositionPoint;
  pilot?: ScreenPositionPoint;
  home?: ScreenPositionPoint;
  droneTrajectory?: ScreenPositionTrackPoint[];
  pilotTrajectory?: ScreenPositionTrackPoint[];
  pilotDistanceM?: number;
  droneDistanceM?: number;
  droneDirectionDeg?: number;
  deviceDirectionDeg?: number;
  height?: number;
  altitude?: number;
  speed?: number;
  lastRecord: ScreenPositionLastRecord;
  archivedAt: string;
}

export interface IntrusionDeleteRequest {
  ids: string[];
}

export interface IntrusionDeleteResponse {
  deleted: number;
}

export interface ScreenFPVLastRecord {
  format: string;
  receivedAt: string;
  frequency: number;
  rssi: number;
  signalType: string;
  valid: boolean;
  deviceSn?: string;
  raw?: string;
}

export interface ScreenFPVTarget {
  id: string;
  frequency: number;
  rssi: number;
  signalType: string;
  valid: boolean;
  deviceSn?: string;
  format: string;
  firstSeen: string;
  lastSeen: string;
  hitCount: number;
  lastRecord: ScreenFPVLastRecord;
}

export type FPVVideoRecordStatus = "ready" | "failed" | string;

export interface FPVVideoRecord {
  id: string;
  targetId?: string;
  frequency: number;
  rssi: number;
  signalType?: string;
  deviceSn?: string;
  startedAt: string;
  endedAt: string;
  durationSeconds: number;
  status: FPVVideoRecordStatus;
  fileName?: string;
  fileSizeBytes?: number;
  fileUrl?: string;
  error?: string;
  lastRecord: ScreenFPVLastRecord;
}

export interface FPVVideoRecordDeleteRequest {
  ids: string[];
}

export interface FPVVideoRecordDeleteResponse {
  deleted: number;
}

export interface GpioChannel {
  id: string;
  label: string;
  pin: number;
  bands: string[];
  reserved: boolean;
  enabled: boolean;
  actualLevel: string;
  desiredLevel: string;
  status: string;
  lastError?: string;
}

export interface ScreenStrikeRequest {
  enabled: boolean;
  channelIds: string[];
  durationSeconds: number;
}

export interface ScreenStrikeState {
  active: boolean;
  channelIds: string[];
  durationSeconds: number;
  remainingSeconds: number;
  startedAt?: string;
  endsAt?: string;
  channels: GpioChannel[];
}

export interface ScreenStrikeResponse {
  state: ScreenStrikeState;
  message: string;
}

export type InterferenceReportStatus = "running" | "completed" | "failed" | "abnormal" | string;

export interface InterferenceReportSummary {
  id: string;
  status: InterferenceReportStatus;
  startedAt: string;
  endedAt?: string;
  durationSeconds: number;
  requestedDurationSeconds?: number;
  channelIds?: string[];
  channelLabels?: string[];
  channelPins?: number[];
  summary?: string;
  lastError?: string;
  abnormalReason?: string;
  createdAt: string;
  updatedAt: string;
}

export interface InterferenceReport {
  id: string;
  status: InterferenceReportStatus;
  startedAt: string;
  endedAt?: string;
  durationSeconds: number;
  requestedDurationSeconds?: number;
  channelIds?: string[];
  channelLabels?: string[];
  channelPins?: number[];
  summary?: string;
  lastError?: string;
  abnormalReason?: string;
  createdAt: string;
  updatedAt: string;
  request: ScreenStrikeRequest;
  startState?: ScreenStrikeState;
  endState?: ScreenStrikeState;
}

export interface InterferenceReportDeleteResponse {
  deleted: number;
}

export interface EventMessage<T> {
  type: string;
  time: string;
  payload?: T;
}

export interface ListResponse<T> {
  items: T[];
  count: number;
  hasMore?: boolean;
  nextOffset?: number;
}
