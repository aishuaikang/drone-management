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

export interface OfflineMapUploadLog {
  stage: string;
  message: string;
  status: "pending" | "running" | "success" | "error" | string;
  timestamp: string;
  detail?: string;
}

export interface OfflineMapUploadResponse {
  map: OfflineMapStatus;
  message: string;
  logs?: OfflineMapUploadLog[];
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

export interface TCPClientStatus {
  address: string;
  host: string;
  port: number;
  connected: boolean;
  connectError?: string;
  updatedAt?: string;
}

export interface ScreenRuntimeStatus {
  position: TCPListenerStatus;
  fpv: TCPListenerStatus;
  interference: TCPClientStatus;
  deviceTargetAddress: string;
  fpvVideo: FPVVideoStatus;
  lingyun: LingyunStatus;
  serverTime: string;
}

export type LingyunDeviceType = "aoa" | "dcd" | "rid" | "ifr";

export interface LingyunStatus {
  enabled: boolean;
  configured: boolean;
  connected: boolean;
  connecting?: boolean;
  clientId?: string;
  broker?: string;
  lastError?: string;
  updatedAt?: string;
  devices?: LingyunDeviceStatus[];
}

export interface LingyunDeviceStatus {
  type: LingyunDeviceType | string;
  abbr: string;
  deviceId?: string;
  enabled: boolean;
  reportingEnabled: boolean;
  workState: number;
  lastRegisterAt?: string;
  lastStatusAt?: string;
  lastDataAt?: string;
  lastControlAt?: string;
  lastControlResult?: string;
  lastError?: string;
  publishLogs?: LingyunPublishLog[];
}

export interface LingyunPublishLog {
  kind: string;
  topic: string;
  payload?: string;
  success: boolean;
  at: string;
  error?: string;
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
  lingyun?: LingyunSettings;
  screenStrikeChannelLabels?: string[];
  screenStrikeUnattended?: ScreenStrikeUnattendedConfig;
  warningZoneEnabled?: boolean;
  warningZoneRadiusMeters?: number;
  warningZones?: WarningZone[];
  whitelist?: WhitelistItem[];
}

export interface LingyunSettings {
  enabled: boolean;
  broker?: string;
  clientId?: string;
  username?: string;
  password?: string;
  providerCode?: string;
  protocolVersion?: string;
  publishMinIntervalSeconds?: number;
  registerIntervalSeconds?: number;
  statusIntervalSeconds?: number;
  devices?: LingyunDeviceSettings[];
}

export interface LingyunDeviceSettings {
  type: LingyunDeviceType | string;
  enabled: boolean;
  deviceId?: string;
  deviceName?: string;
  deviceLongitude?: number;
  deviceLatitude?: number;
  deviceAltitude?: number;
  installMode?: number;
  detectionRange?: number;
  horizontalCoverageStartAngle?: number;
  horizontalCoverageEndAngle?: number;
  detectionFrequency?: string[];
  bandWidth?: string;
  countermeasureRange?: number;
  verticalCoverageStartAngle?: number;
  verticalCoverageEndAngle?: number;
  bands?: string[];
  ifrTypes?: number[];
  antennaType?: number;
  activeAntennaType?: number;
  deviceSpec?: LingyunDeviceSpec;
}

export interface LingyunDeviceSpec {
  devModel?: string;
  devMfr?: string;
  devSN?: string;
  devHWVer?: string;
  devSoftVer?: string;
  instLoc?: string;
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

export interface InterferenceChannel {
  id: string;
  label: string;
  output: number;
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

export interface ScreenStrikeUnattendedConfig {
  enabled: boolean;
  channelIds: string[];
  durationSeconds: number;
}

export interface ScreenStrikeUnattendedState extends ScreenStrikeUnattendedConfig {
  phase: "disabled" | "watching" | "striking" | "resting" | string;
  targetPresent: boolean;
  lastCheckedAt?: string;
  nextCheckAt?: string;
  lastError?: string;
}

export interface ScreenStrikeState {
  active: boolean;
  channelIds: string[];
  durationSeconds: number;
  remainingSeconds: number;
  startedAt?: string;
  channels: InterferenceChannel[];
  unattended: ScreenStrikeUnattendedState;
}

export interface ScreenStrikeResponse {
  state: ScreenStrikeState;
  message: string;
}

export type InterferenceReportStatus = "running" | "completed" | "failed" | "abnormal" | string;
export type InterferenceOperationType = "manual" | "unattended" | string;

export interface InterferenceReportSummary {
  id: string;
  status: InterferenceReportStatus;
  operationType?: InterferenceOperationType;
  startedAt: string;
  endedAt?: string;
  durationSeconds: number;
  requestedDurationSeconds?: number;
  channelIds?: string[];
  channelLabels?: string[];
  channelOutputs?: number[];
  summary?: string;
  lastError?: string;
  abnormalReason?: string;
  createdAt: string;
  updatedAt: string;
}

export interface InterferenceReport {
  id: string;
  status: InterferenceReportStatus;
  operationType?: InterferenceOperationType;
  startedAt: string;
  endedAt?: string;
  durationSeconds: number;
  requestedDurationSeconds?: number;
  channelIds?: string[];
  channelLabels?: string[];
  channelOutputs?: number[];
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

export interface NetworkConfig {
  content: string;
  path: string;
  exists: boolean;
}

export interface NetworkInterfaceStatus {
  name: string;
  state: string;
  isUp: boolean;
  mac: string;
  ip: string;
  gateway: string;
  metric: string;
}

export interface NetworkRoute {
  destination: string;
  gateway?: string;
  metric?: string;
  interface?: string;
  protocol?: string;
  scope?: string;
}

export interface NetworkConnectivity {
  defaultGateway: string;
  gatewayReachable: boolean;
  internetReachable: boolean;
  dnsWorking: boolean;
}

export interface NetworkDNSDiagnostics {
  resolvConf: string;
  systemdResolved: boolean;
  resolvectlStatus?: string;
  dnsServers: string;
  testResult: string;
  pingTest: string;
  resolvConfLink: string;
}

export interface NetworkServiceStatus {
  name: string;
  displayName: string;
  active: boolean;
  enabled: boolean;
  masked: boolean;
  shouldRun: boolean;
  isCorrect: boolean;
}

export interface NetworkDiagnostics {
  cloudInitEnabled: boolean;
  networkManagerActive: boolean;
  systemdNetworkdActive: boolean;
  ifupdownConfigured: boolean;
  netplanFiles: string[];
  activeRenderer: string;
  conflicts: string[];
  recommendations: string[];
  serviceStatuses: NetworkServiceStatus[];
}

export interface NetworkBackup {
  name: string;
  createdAt: string;
  size: number;
}
