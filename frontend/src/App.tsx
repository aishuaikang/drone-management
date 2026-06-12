import {
  Antenna,
  BellRing,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Crosshair,
  LocateFixed,
  Download,
  Edit3,
  Globe2,
  HardDriveUpload,
  Info,
  ListFilter,
  Loader2,
  MapPinned,
  Maximize2,
  MapPin,
  Play,
  FileVideo,
  RefreshCw,
  Plus,
  QrCode,
  Radio,
  Satellite,
  Search,
  Settings,
  ShieldCheck,
  ShieldMinus,
  ShieldPlus,
  Signal,
  Square,
  TimerReset,
  Trash2,
  Volume2,
  VolumeX,
  X,
  Zap,
} from "lucide-react";
import L from "leaflet";
import * as QRCode from "qrcode";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

import {
  FPV_VIDEO_SESSION_BUSY_CODE,
  clearManualDeviceLocation,
  closeFPVVideoSessionEventually,
  deleteFailedInterferenceReport,
  deleteFPVVideoRecords,
  exportFPVVideoRecords,
  getFPVVideoRecords,
  getInterferenceReports,
  deleteIntrusions,
  getIntrusions,
  getOfflineMapStatus,
  getScreenDeviceLocation,
  getScreenFPV,
  getScreenPositions,
  getScreenStatus,
  getScreenStrike,
  getUserSettings,
  openFPVVideoSession,
  openScreenStream,
  setManualDeviceLocation,
  updateScreenTCPPorts,
  updateScreenStrike,
  uploadOfflineMap,
  updateUserSettings,
} from "./api";
import type { InterferenceReportQuery, IntrusionQuery } from "./api";
import { VirtualKeyboard } from "./components/VirtualKeyboard";
import centerPointIcon from "./assets/images/centerPoint.svg";
import detectionDeviceIconOnlineUrl from "./assets/images/detectionDeviceIconOnline.svg";
import screenAlarmAudio from "./assets/images/screen/audio.mp3";
import footerBg from "./assets/images/screen/footerBg.svg?raw";
import headerBg from "./assets/images/screen/headerBg.svg?raw";
import mini2Image from "./assets/images/uav/mini2.png";
import remoteControlBlackFlyIconUrl from "./assets/images/remoteControlBlackFlyIcon.svg";
import remoteControlIconUrl from "./assets/images/remoteControlIcon.svg";
import selectedRemoteControlBlackFlyIconUrl from "./assets/images/selectedRemoteControlBlackFlyIcon.svg";
import selectedRemoteControlIconUrl from "./assets/images/selectedRemoteControlIcon.svg";
import selectedUavBlackFlyIconUrl from "./assets/images/selectedUavBlackFlyIcon.svg";
import selectedUavIconUrl from "./assets/images/selectedUavIcon.svg";
import uavBlackFlyIconUrl from "./assets/images/uavBlackFlyIcon.svg";
import uavIconUrl from "./assets/images/uavIcon.svg";
import type {
  GeoPoint,
  FPVVideoRecord,
  InterferenceChannel,
  InterferenceReportStatus,
  InterferenceReportSummary,
  IntrusionRecord,
  OfflineMapStatus,
  ScreenDeviceLocationResponse,
  ScreenFPVTarget,
  ScreenPositionPoint,
  ScreenPositionTarget,
  ScreenPositionTrackPoint,
  ScreenRuntimeStatus,
  ScreenStrikeState,
  TCPClientStatus,
  TCPListenerStatus,
  UserSettings,
  WarningZone,
  WhitelistItem,
} from "./types";
import { createDrawControlButtonGroup } from "./utils/leafletControls";
import { installLeafletCoordConverter } from "./utils/leafletCoordConverter";

type Locale = "zh-CN" | "en-US";
type Tab = "positions" | "fpv";
type View = "screen" | "intrusions" | "fpvRecords" | "interferenceReports" | "whitelist" | "settings" | "offlineMap";
type CSVCell = string | number | null | undefined;
type NavigationMapProvider = "amap" | "google";
type NavigationCoordinateSystem = "WGS84" | "GCJ-02";
type ReferenceMapLayer =
  | "leaflet.map.gaodeMap"
  | "leaflet.map.gaodeSatellite"
  | "leaflet.map.googleMap"
  | "leaflet.map.googleSatellite"
  | "leaflet.map.offlineMap";
type NavigationQRCodeItem = {
  provider: NavigationMapProvider;
  labelKey: string;
  url: string;
  dataUrl: string;
  coordinate: ScreenPositionPoint;
  coordinateSystem: NavigationCoordinateSystem;
  coordinateLabelKey: string;
};
type NavigationQRCodeState = {
  label: string;
  point: ScreenPositionPoint;
  convertedPoint: ScreenPositionPoint;
  items: NavigationQRCodeItem[];
};
type ManualLocationDraft = {
  latitude: string;
  longitude: string;
};

type ScreenMapData = {
  deviceLocation: ScreenDeviceLocationResponse | null;
  positions: ScreenPositionTarget[];
  warningZone: WarningZone | null;
};

const virtualKeyboardLocaleOptions = ["zh-CN", "en-US"] as const;

installLeafletCoordConverter();

const targetLimit = 100;
const defaultPositionExpireSeconds = 5;
const minPositionExpireSeconds = 1;
const maxPositionExpireSeconds = 3600;
const minTCPPort = 1;
const maxTCPPort = 65535;
const fpvTargetExpireMs = 10_000;
const screenStrikeDefaultDurationSeconds = 60;
const screenStrikeMinDurationSeconds = 10;
const screenStrikeMaxDurationSeconds = 180;
const screenStrikeDurationPresets = [10, 30, 60, 90, 120, 180];
const defaultWarningZoneRadiusMeters = 500;
const minWarningZoneRadiusMeters = 10;
const maxWarningZoneRadiusMeters = 50000;
const referenceMapCenter: L.LatLngTuple = [39.909181, 116.397472];
const referenceMapZoom = 13;
const referenceMapLayerStorageKey = "dr600ab.mapLayer";
const referenceLegacyMapLayerStorageKey = "mapLayer";
const screenAlarmSoundStorageKey = "dr600ab.soundAlarmEnabled";
const referenceDefaultMapLayer: ReferenceMapLayer = "leaflet.map.gaodeSatellite";
const referenceMapLayers: ReferenceMapLayer[] = [
  "leaflet.map.googleMap",
  "leaflet.map.googleSatellite",
  "leaflet.map.gaodeMap",
  "leaflet.map.gaodeSatellite",
  "leaflet.map.offlineMap",
];
const deviceIconSize: [number, number] = [40, 52];
const targetIconSize: [number, number] = [32, 52];
const droneTrackColor = "#26c9ff";
const pilotTrackColor = "#f4c95d";
const warningZoneControlIcon = `
  <span class="warning-zone-button__icon" aria-hidden="true">
    <svg viewBox="0 0 24 24" focusable="false">
      <circle cx="12" cy="12" r="8.2" />
      <circle cx="12" cy="12" r="2.1" />
      <path d="M12 3.2v3.2M12 17.6v3.2M3.2 12h3.2M17.6 12h3.2" />
    </svg>
  </span>
`;
const navigationMapProviders: Array<{ id: NavigationMapProvider; labelKey: string }> = [
  { id: "google", labelKey: "leaflet.map.googleMap" },
  { id: "amap", labelKey: "leaflet.map.gaodeMap" },
];

const labels: Record<Locale, Record<string, string>> = {
  "zh-CN": {
    title: "无人机智能管控系统",
    subtitle: "定位与 FPV 图传信号接收",
    targetIp: "目标 IP",
    networkStatus: "网口状态",
    positionTcp: "定位 TCP",
    fpvTcp: "FPV TCP",
    connected: "已连接",
    listening: "监听中",
    offline: "未监听",
    waiting: "等待设备",
    positions: "定位列表",
    fpv: "FPV 图传",
    emptyPositions: "暂无定位目标",
    emptyFPV: "暂无 FPV 图传信号",
    serial: "序列号",
    fingerprint: "电子指纹",
    model: "型号",
    source: "来源",
    linkStatus: "链路状态",
    deviceInfo: "设备信息",
    drone: "无人机",
    pilot: "飞手",
    home: "返航点",
    pilotDistance: "飞手距离",
    droneDistance: "无人机距离",
    altitude: "海拔",
    height: "高度",
    speed: "速度",
    frequency: "频点",
    rssi: "强度",
    lastSeen: "最近更新",
    firstSeen: "首次出现",
    deviceLocation: "设备位置",
    manualLocation: "手动定位",
    setManualLocation: "设置手动定位",
    editManualLocation: "修改手动定位",
    clearManualLocation: "清除手动定位",
    manualLocationTitle: "手动设置设备定位",
    latitude: "纬度",
    longitude: "经度",
    pickManualLocation: "点选手动定位",
    cancelPickManualLocation: "取消点选",
    manualLocationPickHint: "点击地图设置设备经纬度",
    save: "保存",
    clear: "清除",
    cancel: "取消",
    manualLocationInvalid: "请输入有效的经纬度",
    manualLocationSaveFailed: "手动定位保存失败",
    locked: "已锁定",
    unlocked: "未锁定",
    noLocation: "无定位",
    parsingTarget: "解析中",
    targetDisappearCountdown: "目标即将消失",
    navigationQRCode: "导航二维码",
    navigationCoordinateOriginal: "原始坐标",
    navigationCoordinateConverted: "转换坐标",
    navigationCoordinateSystemWGS84: "WGS84",
    navigationCoordinateSystemGCJ02: "GCJ-02",
    generatingQRCode: "正在生成二维码",
    scanToNavigate: "扫码后可用地图导航到该坐标",
    generateNavigationQRCodeFailed: "二维码生成失败",
    latitudeShort: "纬",
    longitudeShort: "经",
    close: "关闭",
    rfTemp: "射频温度",
    mainTemp: "主控温度",
    valid: "有效",
    invalid: "无效",
    format: "格式",
    viewVideo: "查看视频",
    fpvVideo: "FPV 视频",
    videoLoading: "正在连接视频流",
    videoUnavailable: "未配置视频",
    videoBusy: "已有客户端正在查看视频",
    videoError: "视频流暂不可用",
    videoUnsupported: "当前浏览器不支持该视频流",
    play: "播放",
    pause: "暂停",
    fullscreen: "全屏",
    receiver: "接收端",
    signal: "信号",
    client: "设备连接",
    language: "语言",
    meters: "米",
    metersPerSecond: "米/秒",
    seconds: "秒",
    secondsAgo: "秒前",
    minutesAgo: "分钟前",
    justNow: "刚刚",
    unknown: "未知",
    allClear: "链路正常",
    stream: "实时流",
    center: "回到中心",
    layerSatellite: "卫星图",
    layerMap: "标准图",
    "leaflet.map.gaodeMap": "高德地图",
    "leaflet.map.gaodeSatellite": "高德卫星地图",
    "leaflet.map.googleMap": "谷歌地图",
    "leaflet.map.googleSatellite": "谷歌卫星地图",
    "leaflet.map.offlineMap": "离线地图",
    map: "地图",
    mapLegend: "图例",
    warningZone: "预警圈",
    enableWarningZone: "开启预警圈",
    disableWarningZone: "关闭预警圈",
    warningZoneRadius: "预警圈半径",
    warningZoneRadiusHint: "预警圈以设备 GPS 为中心，设备移动时圆心同步移动。",
    warningZoneRadiusInvalid: "请输入 10 到 50000 米之间的半径",
    warningZoneNoDeviceLocation: "设备无有效 GPS，预警圈暂不显示",
    whitelistDrone: "无人机（白名单）",
    unwhitelistedDrone: "无人机（未入白名单）",
    whitelistPilot: "飞手（白名单）",
    unwhitelistedPilot: "飞手（未入白名单）",
	    trajectory: "无人机轨迹",
	    pilotTrajectory: "飞手轨迹",
	    trajectoryReplay: "轨迹回放",
	    time: "时间",
    coordinate: "坐标",
    deviceStatus: "设备状态",
    tcpAddress: "监听地址",
    connectedClient: "来源",
    expandPanel: "展开面板",
    collapsePanel: "收起面板",
    kilometers: "公里",
    screenView: "实时大屏",
    intrusionsView: "目标入侵",
    fpvRecordsView: "图传记录",
    interferenceReportsView: "干扰报告",
    whitelistView: "白名单",
    settingsView: "设置",
    offlineMapView: "离线地图",
    settingsTitle: "大屏设置",
    displaySettings: "显示设置",
    customScreenTitle: "大屏标题",
    customScreenTitleHint: "留空后使用当前语言默认标题。",
    tcpPortSettings: "TCP 接收端口",
    tcpPortSettingsHint: "保存后立即重启定位与 FPV 数据监听。",
    positionTCPPort: "定位模块端口",
    fpvTCPPort: "FPV 模块端口",
    tcpPortInvalid: "请输入 1 到 65535 之间且不重复的端口",
    positionExpireSettings: "定位过期设置",
    positionExpireSeconds: "定位过期秒数",
    positionExpireHint: "控制定位目标从大屏消失并归档为入侵记录的时间，默认 5 秒。",
    savedValue: "当前保存值",
    preview: "预览",
    restoreDefault: "恢复默认",
    settingsSaved: "设置已保存",
    positionExpireInvalid: "请输入 1 到 3600 秒之间的数值",
    offlineMapTitle: "离线地图上传",
    offlineMapDescription: "上传 ZIP 地图包并切换 /map/dt 瓦片目录",
    offlineMapStatus: "地图状态",
    offlineMapAvailable: "已安装",
    offlineMapUnavailable: "未安装",
    offlineMapTiles: "瓦片数量",
    offlineMapUploadedAt: "上传时间",
    offlineMapSourceFile: "来源文件",
    offlineMapPath: "地图目录",
    offlineMapFile: "地图 ZIP",
    offlineMapSelectFile: "选择 .zip 文件",
    offlineMapKeepBackup: "保留旧地图备份",
    offlineMapUpload: "上传地图",
    offlineMapUploadSuccess: "离线地图上传完成",
    offlineMapUploadFailed: "离线地图上传失败",
    screenStrikeSettings: "干扰设置",
    screenStrikeChannelLabels: "干扰频段标签",
    screenStrikeChannelLabelsHint: "配置大屏干扰按钮和干扰报告里展示的八路频段名称。",
    screenStrikeChannelLabel: "频段 {index}",
    strike: "干扰",
    operationPanel: "操作面板",
    startStrike: "开启干扰",
    stopStrike: "停止干扰",
    strikeChannels: "干扰通道",
    strikeDuration: "干扰时长",
    strikeRemaining: "剩余时间",
    strikeSelectRequired: "请选择至少一个干扰通道",
    strikeActive: "干扰中",
    strikeInactive: "待机",
    strikeDurationInvalid: "干扰时长需在 10 秒到 3 分钟之间",
    interferenceReportList: "干扰报告列表",
    noInterferenceReports: "暂无干扰报告",
    interferenceReportStatusAll: "全部",
    interferenceReportStatusRunning: "运行中",
    interferenceReportStatusCompleted: "已完成",
    interferenceReportStatusFailed: "失败",
    interferenceReportStatusAbnormal: "异常",
    interferenceReportChannels: "通道",
    interferenceReportRequestedDuration: "请求时长",
    interferenceReportError: "错误",
    deleteFailedReport: "删除失败报告",
    deleteInterferenceReportTitle: "删除干扰报告",
    deleteInterferenceReportMessage: "确定删除这条失败的干扰报告吗？删除后无法恢复。",
    intrusionList: "目标入侵列表",
    fpvRecordList: "FPV 图传记录",
    intrusionMapTitle: "入侵坐标地图",
    whitelistManagement: "白名单管理",
    filter: "筛选",
    modelFilter: "型号",
    serialFilter: "序列号",
    dateFrom: "开始日期",
    dateTo: "结束日期",
    clearFilters: "清空",
    loadMore: "加载更多",
    noIntrusions: "暂无入侵记录",
    noFPVVideoRecords: "暂无图传记录",
    noWhitelist: "暂无白名单目标",
    refresh: "刷新",
    recordCount: "记录",
    selectedCount: "已选",
    trajectoryCount: "轨迹点",
    recordId: "记录ID",
    targetId: "目标ID",
    trackType: "轨迹类型",
    pointIndex: "点序号",
    trackPointTime: "轨迹时间",
    speedMetersPerSecond: "速度(m/s)",
    heightMeters: "高度(米)",
    deleteSelected: "删除选中",
    exportReport: "导出报告",
    exportVideoFiles: "导出视频",
    exporting: "导出中",
    exportEmpty: "没有可导出的记录",
    exportVideoEmpty: "没有可导出的视频文件",
    exportFailed: "导出失败",
    deleteConfirmTitle: "删除目标入侵记录",
    deleteConfirmMessage: "确定删除选中的 {count} 条目标入侵记录吗？删除后无法恢复。",
    deleteFPVRecordTitle: "删除图传记录",
    deleteFPVRecordMessage: "确定删除选中的 {count} 条图传记录和视频文件吗？删除后无法恢复。",
    deletedRecords: "已删除记录",
    archivedAt: "归档时间",
    duration: "持续",
    actions: "操作",
    status: "状态",
    fileSize: "文件大小",
    playVideoFile: "播放文件",
    viewRecord: "查看",
    failureReason: "失败原因",
    recording: "录制中",
    recordReady: "可播放",
    recordFailed: "失败",
    signalTypeFilter: "信号类型",
    createdAt: "创建时间",
    whitelist: "白名单",
    whitelisted: "已在白名单",
    addToWhitelist: "加入白名单",
    addToWhitelistShort: "加入",
    removeFromWhitelist: "移出白名单",
    removeFromWhitelistShort: "移出",
    whitelistSerialRequired: "请输入序列号",
    whitelistSaved: "白名单已保存",
    whitelistDeleted: "白名单已删除",
    saveFailed: "保存失败",
    deleteFailed: "删除失败",
    add: "新增",
    edit: "编辑",
    delete: "删除",
    alarm: "告警",
    activeAlarmTargets: "未入白名单目标",
    soundAlarm: "声音报警",
    soundAlarmOn: "声音报警已开启",
    soundAlarmOff: "声音报警已静音",
    enableSoundAlarm: "启用声音告警",
    soundAlarmBlocked: "浏览器已阻止自动播放，请点击启用声音告警",
    muteSoundAlarm: "静音声音告警",
    unmuteSoundAlarm: "开启声音告警",
    "keyboard.virtualKeyboard": "虚拟键盘",
    "keyboard.chineseCandidates": "中文候选词",
    "keyboard.clear": "清空",
    "keyboard.enter": "确认",
    "keyboard.close": "关闭",
    "keyboard.space": "空格",
    "keyboard.pinyinInput": "拼音输入",
    "keyboard.dictionaryLoading": "加载 Rime 词库",
    "keyboard.pinyinHint": "输入拼音选择候选",
  },
  "en-US": {
    title: "UAV Control System",
    subtitle: "Position and FPV signal receiver",
    targetIp: "Target IP",
    networkStatus: "Network status",
    positionTcp: "Position TCP",
    fpvTcp: "FPV TCP",
    connected: "Connected",
    listening: "Listening",
    offline: "Offline",
    waiting: "Waiting",
    positions: "Positions",
    fpv: "FPV Signals",
    emptyPositions: "No position targets",
    emptyFPV: "No FPV signals",
    serial: "Serial",
    fingerprint: "Fingerprint",
    model: "Model",
    source: "Source",
    linkStatus: "Link status",
    deviceInfo: "Device info",
    drone: "Drone",
    pilot: "Pilot",
    home: "Home",
    pilotDistance: "Pilot distance",
    droneDistance: "Drone distance",
    altitude: "Altitude",
    height: "Height",
    speed: "Speed",
    frequency: "Frequency",
    rssi: "RSSI",
    lastSeen: "Last seen",
    firstSeen: "First seen",
    deviceLocation: "Device",
    manualLocation: "Manual",
    setManualLocation: "Set manual location",
    editManualLocation: "Edit manual location",
    clearManualLocation: "Clear manual location",
    manualLocationTitle: "Set Device Location",
    latitude: "Latitude",
    longitude: "Longitude",
    pickManualLocation: "Pick manual location",
    cancelPickManualLocation: "Cancel picking",
    manualLocationPickHint: "Click the map to set device coordinates",
    save: "Save",
    clear: "Clear",
    cancel: "Cancel",
    manualLocationInvalid: "Enter valid latitude and longitude",
    manualLocationSaveFailed: "Failed to save manual location",
    locked: "Locked",
    unlocked: "Unlocked",
    noLocation: "No fix",
    parsingTarget: "Parsing",
    targetDisappearCountdown: "Target expires",
    navigationQRCode: "Navigation QR",
    navigationCoordinateOriginal: "Original coordinate",
    navigationCoordinateConverted: "Converted coordinate",
    navigationCoordinateSystemWGS84: "WGS84",
    navigationCoordinateSystemGCJ02: "GCJ-02",
    generatingQRCode: "Generating QR code",
    scanToNavigate: "Scan to navigate to this coordinate",
    generateNavigationQRCodeFailed: "Failed to generate QR code",
    latitudeShort: "Lat",
    longitudeShort: "Lng",
    close: "Close",
    rfTemp: "RF temp",
    mainTemp: "Main temp",
    valid: "Valid",
    invalid: "Invalid",
    format: "Format",
    viewVideo: "View video",
    fpvVideo: "FPV video",
    videoLoading: "Connecting video stream",
    videoUnavailable: "Video not configured",
    videoBusy: "Another client is viewing video",
    videoError: "Video stream unavailable",
    videoUnsupported: "This browser cannot play the stream",
    play: "Play",
    pause: "Pause",
    fullscreen: "Fullscreen",
    receiver: "Receiver",
    signal: "Signal",
    client: "Source",
    language: "Language",
    meters: "m",
    metersPerSecond: "m/s",
    seconds: "s",
    secondsAgo: "s ago",
    minutesAgo: "m ago",
    justNow: "now",
    unknown: "Unknown",
    allClear: "Links normal",
    stream: "Live stream",
    center: "Center map",
    layerSatellite: "Satellite",
    layerMap: "Map",
    "leaflet.map.gaodeMap": "Gaode Map",
    "leaflet.map.gaodeSatellite": "Gaode Satellite",
    "leaflet.map.googleMap": "Google Map",
    "leaflet.map.googleSatellite": "Google Satellite",
    "leaflet.map.offlineMap": "Offline Map",
    map: "Map",
    mapLegend: "Legend",
    warningZone: "Warning zone",
    enableWarningZone: "Enable warning zone",
    disableWarningZone: "Disable warning zone",
    warningZoneRadius: "Warning zone radius",
    warningZoneRadiusHint: "The warning zone uses device GPS as center and moves with the device.",
    warningZoneRadiusInvalid: "Enter a radius from 10 to 50000 meters",
    warningZoneNoDeviceLocation: "Device GPS unavailable; warning zone hidden",
    whitelistDrone: "Drone (whitelist)",
    unwhitelistedDrone: "Drone (alert)",
    whitelistPilot: "Pilot (whitelist)",
    unwhitelistedPilot: "Pilot (alert)",
	    trajectory: "Drone track",
	    pilotTrajectory: "Pilot track",
	    trajectoryReplay: "Track Replay",
	    time: "Time",
    coordinate: "Coordinate",
    deviceStatus: "Device status",
    tcpAddress: "Listen address",
    connectedClient: "Client",
    expandPanel: "Expand panel",
    collapsePanel: "Collapse panel",
    kilometers: "km",
    screenView: "Live Screen",
    intrusionsView: "Intrusions",
    fpvRecordsView: "Video Records",
    interferenceReportsView: "Strike Reports",
    whitelistView: "Whitelist",
    settingsView: "Settings",
    offlineMapView: "Offline Map",
    settingsTitle: "Screen Settings",
    displaySettings: "Display",
    customScreenTitle: "Screen title",
    customScreenTitleHint: "Leave empty to use the default title for the current language.",
    tcpPortSettings: "TCP receive ports",
    tcpPortSettingsHint: "Saving immediately restarts positioning and FPV listeners.",
    positionTCPPort: "Position module port",
    fpvTCPPort: "FPV module port",
    tcpPortInvalid: "Enter unique ports from 1 to 65535",
    positionExpireSettings: "Position Expiration",
    positionExpireSeconds: "Position expiration seconds",
    positionExpireHint: "Controls when positioning targets disappear and archive as intrusion records. Default is 5 seconds.",
    savedValue: "Saved value",
    preview: "Preview",
    restoreDefault: "Restore default",
    settingsSaved: "Settings saved",
    positionExpireInvalid: "Enter a value from 1 to 3600 seconds",
    offlineMapTitle: "Offline Map Upload",
    offlineMapDescription: "Upload a ZIP map package and switch the /map/dt tile directory",
    offlineMapStatus: "Map status",
    offlineMapAvailable: "Installed",
    offlineMapUnavailable: "Not installed",
    offlineMapTiles: "Tiles",
    offlineMapUploadedAt: "Uploaded at",
    offlineMapSourceFile: "Source file",
    offlineMapPath: "Map path",
    offlineMapFile: "Map ZIP",
    offlineMapSelectFile: "Choose .zip file",
    offlineMapKeepBackup: "Keep previous map backup",
    offlineMapUpload: "Upload map",
    offlineMapUploadSuccess: "Offline map uploaded",
    offlineMapUploadFailed: "Offline map upload failed",
    screenStrikeSettings: "Interference",
    screenStrikeChannelLabels: "Band labels",
    screenStrikeChannelLabelsHint: "Configures the eight band names shown in screen strike controls and reports.",
    screenStrikeChannelLabel: "Band {index}",
    strike: "Strike",
    operationPanel: "Operation",
    startStrike: "Start strike",
    stopStrike: "Stop strike",
    strikeChannels: "Channels",
    strikeDuration: "Duration",
    strikeRemaining: "Remaining",
    strikeSelectRequired: "Select at least one channel",
    strikeActive: "Active",
    strikeInactive: "Idle",
    strikeDurationInvalid: "Duration must be between 10 seconds and 3 minutes",
    interferenceReportList: "Interference Reports",
    noInterferenceReports: "No interference reports",
    interferenceReportStatusAll: "All",
    interferenceReportStatusRunning: "Running",
    interferenceReportStatusCompleted: "Completed",
    interferenceReportStatusFailed: "Failed",
    interferenceReportStatusAbnormal: "Abnormal",
    interferenceReportChannels: "Channels",
    interferenceReportRequestedDuration: "Requested",
    interferenceReportError: "Error",
    deleteFailedReport: "Delete failed report",
    deleteInterferenceReportTitle: "Delete Interference Report",
    deleteInterferenceReportMessage: "Delete this failed interference report? This cannot be undone.",
    intrusionList: "Intrusion List",
    fpvRecordList: "FPV Video Records",
    intrusionMapTitle: "Intrusion Map",
    whitelistManagement: "Whitelist",
    filter: "Filter",
    modelFilter: "Model",
    serialFilter: "Serial",
    dateFrom: "From",
    dateTo: "To",
    clearFilters: "Clear",
    loadMore: "Load more",
    noIntrusions: "No intrusion records",
    noFPVVideoRecords: "No FPV video records",
    noWhitelist: "No whitelist targets",
    refresh: "Refresh",
    recordCount: "Records",
    selectedCount: "Selected",
    trajectoryCount: "Track points",
    recordId: "Record ID",
    targetId: "Target ID",
    trackType: "Track type",
    pointIndex: "Point index",
    trackPointTime: "Track time",
    speedMetersPerSecond: "Speed (m/s)",
    heightMeters: "Height (m)",
    deleteSelected: "Delete selected",
    exportReport: "Export report",
    exportVideoFiles: "Export videos",
    exporting: "Exporting",
    exportEmpty: "No records to export",
    exportVideoEmpty: "No video files to export",
    exportFailed: "Export failed",
    deleteConfirmTitle: "Delete Intrusion Records",
    deleteConfirmMessage: "Delete the selected {count} intrusion records? This cannot be undone.",
    deleteFPVRecordTitle: "Delete Video Records",
    deleteFPVRecordMessage: "Delete the selected {count} FPV video records and files? This cannot be undone.",
    deletedRecords: "Deleted records",
    archivedAt: "Archived",
    duration: "Duration",
    actions: "Actions",
    status: "Status",
    fileSize: "File size",
    playVideoFile: "Play file",
    viewRecord: "View",
    failureReason: "Failure reason",
    recording: "Recording",
    recordReady: "Ready",
    recordFailed: "Failed",
    signalTypeFilter: "Signal",
    createdAt: "Created",
    whitelist: "Whitelist",
    whitelisted: "Whitelisted",
    addToWhitelist: "Add whitelist",
    addToWhitelistShort: "Add",
    removeFromWhitelist: "Remove whitelist",
    removeFromWhitelistShort: "Remove",
    whitelistSerialRequired: "Enter serial",
    whitelistSaved: "Whitelist saved",
    whitelistDeleted: "Whitelist removed",
    saveFailed: "Save failed",
    deleteFailed: "Delete failed",
    add: "Add",
    edit: "Edit",
    delete: "Delete",
    alarm: "Alarm",
    activeAlarmTargets: "Unwhitelisted targets",
    soundAlarm: "Sound alarm",
    soundAlarmOn: "Sound alarm on",
    soundAlarmOff: "Sound alarm muted",
    enableSoundAlarm: "Enable sound alarm",
    soundAlarmBlocked: "Browser blocked autoplay. Click to enable sound alarm.",
    muteSoundAlarm: "Mute sound alarm",
    unmuteSoundAlarm: "Enable sound alarm",
    "keyboard.virtualKeyboard": "Virtual keyboard",
    "keyboard.chineseCandidates": "Chinese candidates",
    "keyboard.clear": "Clear",
    "keyboard.enter": "Enter",
    "keyboard.close": "Close",
    "keyboard.space": "Space",
    "keyboard.pinyinInput": "Pinyin input",
    "keyboard.dictionaryLoading": "Loading Rime dictionary",
    "keyboard.pinyinHint": "Enter pinyin to choose candidates",
  },
};

const droneImageModules = import.meta.glob("./assets/images/drone/*.png", {
  eager: true,
  query: "?url",
  import: "default",
}) as Record<string, string>;

const uavImageModules = import.meta.glob("./assets/images/uav/*.png", {
  eager: true,
  query: "?url",
  import: "default",
}) as Record<string, string>;

const positionModelImageNames: Record<string, string> = {
  "air 3": "dji_air3",
  "air 2s": "mavic3_mavicair2s",
  "dji air 3": "dji_air3",
  "dji air3": "dji_air3",
  "dji air 2s": "mavic3_mavicair2s",
  "dji air2s": "mavic3_mavicair2s",
  "mavic 3": "mavic3",
  "mavic 3 pro": "mavic_3_pro",
  "mavic air 2": "mavic_air2",
  "mavic air 2s": "mavic3_mavicair2s",
  "mini 4 pro": "mini4_pro",
};

export function App() {
  const [locale, setLocale] = useState<Locale>("zh-CN");
  const t = labels[locale];
  const [view, setView] = useState<View>("screen");
  const [status, setStatus] = useState<ScreenRuntimeStatus | null>(null);
  const [positions, setPositions] = useState<ScreenPositionTarget[]>([]);
  const [fpv, setFPV] = useState<ScreenFPVTarget[]>([]);
  const [deviceLocation, setDeviceLocation] = useState<ScreenDeviceLocationResponse | null>(null);
  const [userSettings, setUserSettings] = useState<UserSettings>(() => defaultUserSettings());
  const [strikeState, setStrikeState] = useState<ScreenStrikeState | null>(null);
  const [whitelistBusySerial, setWhitelistBusySerial] = useState("");
  const [selectedPositionId, setSelectedPositionId] = useState("");
  const [tab, setTab] = useState<Tab>("positions");
  const [now, setNow] = useState(() => new Date());
  const [streamError, setStreamError] = useState("");
  const [soundAlarmEnabled, setSoundAlarmEnabled] = useState(() => getStoredSoundAlarmEnabled());
  const [languageOpen, setLanguageOpen] = useState(false);
  const [strikeCollapsed, setStrikeCollapsed] = useState(false);
  const [rightCollapsed, setRightCollapsed] = useState(false);
  const [navigationQRCode, setNavigationQRCode] = useState<NavigationQRCodeState | null>(null);
  const [navigationQRCodeLoading, setNavigationQRCodeLoading] = useState(false);
  const [navigationQRCodeError, setNavigationQRCodeError] = useState("");
  const navigationQRCodeRequestRef = useRef(0);
  const [manualLocationOpen, setManualLocationOpen] = useState(false);
  const [manualLocationPickMode, setManualLocationPickMode] = useState(false);
  const [manualLocationDraft, setManualLocationDraft] = useState<ManualLocationDraft>({ latitude: "", longitude: "" });
  const [manualLocationSaving, setManualLocationSaving] = useState(false);
  const [manualLocationError, setManualLocationError] = useState("");
  const [fpvVideoTarget, setFPVVideoTarget] = useState<ScreenFPVTarget | null>(null);
  const [fpvVideoOpeningId, setFPVVideoOpeningId] = useState("");
  const [fpvVideoSessionToken, setFPVVideoSessionToken] = useState("");
  const [fpvVideoClosing, setFPVVideoClosing] = useState(false);
  const fpvVideoSessionCloseRef = useRef<((notifyBackend?: boolean) => void) | null>(null);
  const fpvVideoPlaybackURL = status?.fpvVideo?.enabled ? status.fpvVideo.playbackUrl ?? "" : "";
  const ownsFPVVideoSession = Boolean(fpvVideoTarget || fpvVideoOpeningId);
  const fpvVideoBusy = Boolean(status?.fpvVideo?.active && !ownsFPVVideoSession);
  const positionExpireSeconds = resolvePositionExpireSeconds(userSettings.positionExpireSeconds);
  const screenTitle = userSettings.screenTitle?.trim() || t.title;
  const [strikeStateSyncedAt, setStrikeStateSyncedAt] = useState(() => Date.now());

  const syncStrikeState = useCallback((nextState: ScreenStrikeState) => {
    setStrikeState(nextState);
    setStrikeStateSyncedAt(Date.now());
  }, []);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    let cancelled = false;
    let syncing = false;

    const sync = async () => {
      if (syncing) {
        return;
      }
      syncing = true;
      try {
        const [statusRes, positionRes, fpvRes, locationRes, settingsRes, strikeRes] = await Promise.all([
          getScreenStatus(),
          getScreenPositions(targetLimit),
          getScreenFPV(targetLimit),
          getScreenDeviceLocation(),
          getUserSettings(),
          getScreenStrike(),
        ]);
        if (!cancelled) {
          setStatus(statusRes);
          setPositions(sortPositions(positionRes.items));
          setFPV(sortFPV(fpvRes.items));
          setDeviceLocation(locationRes);
          setUserSettings(resolveUserSettings(settingsRes));
          syncStrikeState(strikeRes);
          setStreamError("");
        }
      } catch (error) {
        if (!cancelled) {
          setStreamError(error instanceof Error ? error.message : String(error));
        }
      } finally {
        syncing = false;
      }
    };

    void sync();
    const timer = window.setInterval(() => void sync(), 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [syncStrikeState]);

  useEffect(() => {
    return openScreenStream({
      onPosition: (event) => {
        if (event.payload) {
          setStreamError("");
          setPositions((items) => mergePosition(items, event.payload!, targetLimit));
        }
      },
      onPositionRemoved: (event) => {
        if (event.payload) {
          setPositions((items) => removePosition(items, event.payload!));
        }
      },
      onFPV: (event) => {
        if (event.payload) {
          setStreamError("");
          setFPV((items) => mergeFPV(items, event.payload!, targetLimit));
        }
      },
      onDeviceLocation: (event) => {
        if (event.payload) {
          setStreamError("");
          setDeviceLocation(event.payload);
        }
      },
      onStrike: (event) => {
        if (event.payload) {
          syncStrikeState(event.payload);
        }
      },
      onError: (error) => setStreamError(error.message),
    });
  }, [syncStrikeState]);

  useEffect(() => {
    if (!strikeState?.active || view !== "screen") {
      return;
    }

    let cancelled = false;
    let syncing = false;

    const syncStrike = async () => {
      if (syncing) {
        return;
      }
      syncing = true;
      try {
        const nextState = await getScreenStrike();
        if (!cancelled) {
          syncStrikeState(nextState);
        }
      } catch (error) {
        if (!cancelled) {
          setStreamError(error instanceof Error ? error.message : String(error));
        }
      } finally {
        syncing = false;
      }
    };

    void syncStrike();
    const timer = window.setInterval(() => void syncStrike(), 1000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [strikeState?.active, syncStrikeState, view]);

  const visiblePositions = useMemo(() => filterVisiblePositions(positions, now, positionExpireSeconds), [now, positionExpireSeconds, positions]);
  const selectedPosition = useMemo(
    () => visiblePositions.find((item) => item.id === selectedPositionId) ?? null,
    [selectedPositionId, visiblePositions],
  );
  const visibleFPV = useMemo(() => filterVisibleFPV(fpv, now), [fpv, now]);
  const activeWarningZone = useMemo(() => resolveActiveWarningZone(userSettings, deviceLocation), [deviceLocation, userSettings]);
  const alarmTargetCount = useMemo(
    () => countAlarmPositions(visiblePositions, userSettings.whitelist, activeWarningZone),
    [activeWarningZone, userSettings.whitelist, visiblePositions],
  );
  const alarmSound = useScreenAlarmSound(alarmTargetCount > 0 && soundAlarmEnabled);

  const handleSetSoundAlarmEnabled = useCallback((enabled: boolean) => {
    setSoundAlarmEnabled(enabled);
    persistSoundAlarmEnabled(enabled);
  }, []);

  const handleSelectPosition = useCallback((target: ScreenPositionTarget) => {
    setSelectedPositionId((current) => (current === target.id ? "" : target.id));
    setTab("positions");
    setView("screen");
  }, []);

  const saveUserSettings = useCallback(async (next: UserSettings) => {
    const saved = await updateUserSettings({
      ...userSettings,
      ...next,
      whitelist: next.whitelist ?? userSettings.whitelist ?? [],
    });
    const resolved = resolveUserSettings(saved);
    setUserSettings(resolved);
    return resolved;
  }, [userSettings]);

  const handleTogglePositionWhitelist = useCallback(async (target: ScreenPositionTarget) => {
    const serial = target.serial.trim();
    const whitelisted = isSerialWhitelisted(serial, userSettings.whitelist);
    if (!whitelisted && isPendingEncryptedDJIDrone(target)) {
      setStreamError(t.parsingTarget);
      return;
    }
    if (!serial) {
      setStreamError(t.whitelistSerialRequired);
      return;
    }
    const busyKey = normalizeWhitelistSerial(serial);
    setWhitelistBusySerial(busyKey);
    try {
      const nextWhitelist = whitelisted
        ? removeWhitelistSerial(userSettings.whitelist, serial)
        : upsertWhitelistItem(userSettings.whitelist, {
          serial,
          model: target.model,
          source: target.source || "screen",
        });
      await saveUserSettings({ whitelist: nextWhitelist });
      setStreamError("");
    } catch (error) {
      setStreamError(error instanceof Error ? error.message : t.saveFailed);
    } finally {
      setWhitelistBusySerial("");
    }
  }, [saveUserSettings, t.parsingTarget, t.saveFailed, t.whitelistSerialRequired, userSettings.whitelist]);

  const closeFPVVideoSessionStream = useCallback((notifyBackend = true) => {
    fpvVideoSessionCloseRef.current?.(notifyBackend);
    fpvVideoSessionCloseRef.current = null;
  }, []);

  const setLocalFPVVideoActive = useCallback((active: boolean, frequency?: number) => {
    setStatus((current) => {
      if (!current?.fpvVideo?.enabled) {
        return current;
      }
      return {
        ...current,
        fpvVideo: {
          ...current.fpvVideo,
          active,
          activeFrequency: active ? frequency : undefined,
          activeSince: active ? current.fpvVideo.activeSince ?? new Date().toISOString() : undefined,
        },
      };
    });
  }, []);

  const handleOpenFPVVideo = useCallback((target: ScreenFPVTarget) => {
    if (!fpvVideoPlaybackURL) {
      setStreamError(t.videoUnavailable);
      return;
    }
    if (fpvVideoBusy) {
      setStreamError(t.videoBusy);
      return;
    }
    const frequency = Math.round(target.frequency);
    if (!Number.isFinite(frequency) || frequency <= 0) {
      setStreamError(t.videoError);
      return;
    }
    closeFPVVideoSessionStream();
    setFPVVideoTarget(null);
    setFPVVideoOpeningId(target.id);
    setFPVVideoSessionToken("");
    setFPVVideoClosing(false);
    setStreamError("");
    fpvVideoSessionCloseRef.current = openFPVVideoSession(frequency, target.id, {
      onReady: (event) => {
        setFPVVideoOpeningId("");
        setFPVVideoTarget(target);
        setFPVVideoSessionToken(event.payload?.session ?? "");
        setFPVVideoClosing(false);
        setLocalFPVVideoActive(true, frequency);
        setStreamError("");
      },
      onError: (error) => {
        fpvVideoSessionCloseRef.current = null;
        setFPVVideoOpeningId("");
        setFPVVideoTarget(null);
        setFPVVideoSessionToken("");
        setFPVVideoClosing(false);
        if (error.code === FPV_VIDEO_SESSION_BUSY_CODE) {
          setLocalFPVVideoActive(true);
          setStreamError(t.videoBusy);
          return;
        }
        setLocalFPVVideoActive(false);
        setStreamError(error.message || t.videoError);
      },
      onDisconnect: () => {
        fpvVideoSessionCloseRef.current = null;
        setFPVVideoOpeningId("");
        setFPVVideoTarget(null);
        setFPVVideoSessionToken("");
        setFPVVideoClosing(false);
        setLocalFPVVideoActive(false);
        setStreamError(t.videoError);
      },
    });
  }, [closeFPVVideoSessionStream, fpvVideoBusy, fpvVideoPlaybackURL, setLocalFPVVideoActive, t.videoBusy, t.videoError, t.videoUnavailable]);

  const handleCloseFPVVideo = useCallback(() => {
    const token = fpvVideoSessionToken;
    if (document.fullscreenElement) {
      void document.exitFullscreen().catch(() => undefined);
    }
    closeFPVVideoSessionStream(false);
    if (token) {
      closeFPVVideoSessionEventually(token);
    }
    setFPVVideoOpeningId("");
    setFPVVideoTarget(null);
    setFPVVideoSessionToken("");
    setFPVVideoClosing(false);
    setLocalFPVVideoActive(false);
    setStreamError("");
  }, [closeFPVVideoSessionStream, fpvVideoSessionToken, setLocalFPVVideoActive]);

  useEffect(() => {
    return () => closeFPVVideoSessionStream();
  }, [closeFPVVideoSessionStream]);

  const updateNavigationQRCode = useCallback(async (label: string, point: ScreenPositionPoint) => {
    const requestId = navigationQRCodeRequestRef.current + 1;
    navigationQRCodeRequestRef.current = requestId;
    const coordinates = getNavigationCoordinates(point);
    const pendingState: NavigationQRCodeState = {
      label,
      point: coordinates.original,
      convertedPoint: coordinates.converted,
      items: navigationMapProviders.map((provider) => ({
        provider: provider.id,
        labelKey: provider.labelKey,
        url: buildNavigationUrl(coordinates, provider.id),
        dataUrl: "",
        coordinate: provider.id === "google" ? coordinates.original : coordinates.converted,
        coordinateSystem: provider.id === "google" ? "WGS84" : "GCJ-02",
        coordinateLabelKey: provider.id === "google" ? "navigationCoordinateOriginal" : "navigationCoordinateConverted",
      })),
    };

    setNavigationQRCode(pendingState);
    setNavigationQRCodeLoading(true);
    setNavigationQRCodeError("");

    try {
      const nextState = await createNavigationQRCodes(label, point);
      if (navigationQRCodeRequestRef.current !== requestId) {
        return;
      }
      setNavigationQRCode(nextState);
    } catch {
      if (navigationQRCodeRequestRef.current !== requestId) {
        return;
      }
      setNavigationQRCodeError(t.generateNavigationQRCodeFailed);
    } finally {
      if (navigationQRCodeRequestRef.current === requestId) {
        setNavigationQRCodeLoading(false);
      }
    }
  }, [t]);

  const handleOpenNavigationQRCode = useCallback((label: string, point: ScreenPositionPoint) => {
    void updateNavigationQRCode(label, point);
  }, [updateNavigationQRCode]);

  const handleCloseNavigationQRCode = useCallback(() => {
    navigationQRCodeRequestRef.current += 1;
    setNavigationQRCode(null);
    setNavigationQRCodeLoading(false);
    setNavigationQRCodeError("");
  }, []);

  const handleOpenManualLocation = useCallback(() => {
    const point = deviceLocation?.source === "manual" && deviceLocation.point ? deviceLocation.point : null;
    setManualLocationDraft({
      latitude: point ? formatManualCoordinate(point.latitude) : "",
      longitude: point ? formatManualCoordinate(point.longitude) : "",
    });
    setManualLocationError("");
    setManualLocationOpen(true);
  }, [deviceLocation]);

  const handleToggleManualLocationPickMode = useCallback(() => {
    setManualLocationPickMode((enabled) => !enabled);
  }, []);

  const handlePickManualLocation = useCallback((point: GeoPoint) => {
    setManualLocationDraft({
      latitude: formatManualCoordinate(point.latitude),
      longitude: formatManualCoordinate(point.longitude),
    });
    setManualLocationError("");
    setManualLocationPickMode(false);
    setManualLocationOpen(true);
  }, []);

  const handleManualLocationDraftChange = useCallback((field: keyof ManualLocationDraft, value: string) => {
    setManualLocationDraft((current) => ({ ...current, [field]: normalizeCoordinateInput(value) }));
    setManualLocationError("");
  }, []);

  const handleCloseManualLocation = useCallback(() => {
    if (manualLocationSaving) {
      return;
    }
    setManualLocationOpen(false);
    setManualLocationError("");
  }, [manualLocationSaving]);

  const handleSaveManualLocation = useCallback(async () => {
    const latitude = parseCoordinateDraft(manualLocationDraft.latitude);
    const longitude = parseCoordinateDraft(manualLocationDraft.longitude);
    if (!validManualPoint(latitude, longitude)) {
      setManualLocationError(t.manualLocationInvalid);
      return;
    }

    setManualLocationSaving(true);
    setManualLocationError("");
    try {
      const location = await setManualDeviceLocation({ point: { latitude, longitude } });
      setDeviceLocation(location);
      setManualLocationOpen(false);
    } catch {
      setManualLocationError(t.manualLocationSaveFailed);
    } finally {
      setManualLocationSaving(false);
    }
  }, [manualLocationDraft, t]);

  const handleClearManualLocation = useCallback(async () => {
    setManualLocationSaving(true);
    setManualLocationError("");
    try {
      const location = await clearManualDeviceLocation();
      setDeviceLocation(location);
      setManualLocationOpen(false);
    } catch {
      setManualLocationError(t.manualLocationSaveFailed);
    } finally {
      setManualLocationSaving(false);
    }
  }, [t]);

  useEffect(() => {
    if (!navigationQRCode) {
      return;
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        handleCloseNavigationQRCode();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [handleCloseNavigationQRCode, navigationQRCode]);

  useEffect(() => {
    if (!manualLocationOpen) {
      return;
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        handleCloseManualLocation();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [handleCloseManualLocation, manualLocationOpen]);

  return (
    <main className="screen-shell screen-shell--strike-available">
      <ScreenMap
        positions={visiblePositions}
        selectedPosition={selectedPosition}
        deviceLocation={deviceLocation}
        whitelist={userSettings.whitelist}
        warningZone={activeWarningZone}
        warningZoneEnabled={Boolean(userSettings.warningZoneEnabled)}
        manualLocationPickMode={manualLocationPickMode}
        onSelectPosition={handleSelectPosition}
        onManualLocationPick={handlePickManualLocation}
        onWarningZoneToggle={async (enabled) => {
          await saveUserSettings({ warningZoneEnabled: enabled });
        }}
        t={t}
        locale={locale}
      />
      {view === "screen" ? (
        <div className="screen-map-legend-overlay">
          <ScreenMapLegend t={t} />
        </div>
      ) : null}

      <header className="screen-header">
        <span className="screen-header-bg" aria-hidden="true" dangerouslySetInnerHTML={{ __html: headerBg }} />
        <div className="screen-header__left">
          <span className="screen-header__date">{formatScreenDate(now)}</span>
          <strong className="screen-header__time">{now.toLocaleTimeString(locale, { hour12: false })}</strong>
        </div>
        <div className="screen-header__title">
          <h1 title={screenTitle}>{screenTitle}</h1>
        </div>
        <div className="screen-header__right">
          <ViewSwitch view={view} t={t} onViewChange={setView} />
          <div
            className={languageOpen ? "screen-language-switch screen-language-switch--open" : "screen-language-switch"}
            onBlur={(event) => {
              const nextFocus = event.relatedTarget;
              if (!(nextFocus instanceof Node) || !event.currentTarget.contains(nextFocus)) {
                setLanguageOpen(false);
              }
            }}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                setLanguageOpen(false);
              }
            }}
          >
            <button
              className="screen-language-switch__button"
              type="button"
              aria-label={t.language}
              aria-haspopup="listbox"
              aria-expanded={languageOpen}
              onClick={() => setLanguageOpen((value) => !value)}
            >
              <Globe2 aria-hidden="true" />
              <span>{locale === "zh-CN" ? "中文" : "EN"}</span>
              <ChevronDown className="screen-language-switch__arrow" aria-hidden="true" />
            </button>
            {languageOpen ? (
              <div className="screen-language-menu" role="listbox" aria-label={t.language}>
                {(["zh-CN", "en-US"] as Locale[]).map((option) => (
                  <button
                    key={option}
                    className={option === locale ? "screen-language-menu__item screen-language-menu__item--active" : "screen-language-menu__item"}
                    type="button"
                    role="option"
                    aria-selected={option === locale}
                    onClick={() => {
                      setLocale(option);
                      setLanguageOpen(false);
                    }}
                  >
                    {option === "zh-CN" ? "中文" : "English"}
                  </button>
                ))}
              </div>
            ) : null}
          </div>
        </div>
      </header>

      {view === "screen" ? (
      <>
      <ScreenStrikePanel
        state={strikeState}
        stateSyncedAt={strikeStateSyncedAt}
        connectionStatus={status?.interference}
        now={now}
        locale={locale}
        userSettings={userSettings}
        collapsed={strikeCollapsed}
        t={t}
        onStateChange={syncStrikeState}
        onToggleCollapsed={() => setStrikeCollapsed((value) => !value)}
      />
      <aside
        className={rightCollapsed ? "screen-right-panel screen-right-panel--collapsed screen-right-panel--show-toggle" : "screen-right-panel screen-right-panel--show-toggle"}
      >
        <button
          className="screen-side-toggle screen-side-toggle--right"
          type="button"
          aria-label={rightCollapsed ? t.expandPanel : t.collapsePanel}
          onClick={() => setRightCollapsed((value) => !value)}
        >
          {rightCollapsed ? <ChevronLeft aria-hidden="true" /> : <ChevronRight aria-hidden="true" />}
          <span aria-hidden="true" />
        </button>
        <div className="screen-info-list">
          <div className="screen-info-list__header">
            <div className="screen-panel-title">
              <span className="screen-panel-title__icon screen-panel-title__icon--target">
                <Antenna aria-hidden="true" />
              </span>
              <span className="screen-panel-title__text">
                <em>{streamError ? t.stream : t.allClear}</em>
                <strong>{tab === "positions" ? t.positions : t.fpv}</strong>
              </span>
            </div>
            <div className="screen-info-list__counts">
              {streamError ? <span className="screen-stream-error" title={streamError}>!</span> : null}
              {alarmTargetCount > 0 ? (
                <span className="screen-alarm-count" title={`${t.activeAlarmTargets}: ${alarmTargetCount}`}>
                  <BellRing size={12} aria-hidden="true" />
                  <strong>{alarmTargetCount}</strong>
                </span>
              ) : null}
              <button
                type="button"
                className={soundAlarmEnabled ? "screen-sound-toggle screen-sound-toggle--active" : "screen-sound-toggle"}
                aria-pressed={soundAlarmEnabled}
                title={soundAlarmEnabled ? t.muteSoundAlarm : t.unmuteSoundAlarm}
              onClick={() => handleSetSoundAlarmEnabled(!soundAlarmEnabled)}
              >
                {soundAlarmEnabled ? <Volume2 size={13} aria-hidden="true" /> : <VolumeX size={13} aria-hidden="true" />}
              </button>
              <strong className="screen-info-list__count">{tab === "positions" ? visiblePositions.length : visibleFPV.length}</strong>
            </div>
          </div>

          <ScreenAlarmBanner
            activeCount={alarmTargetCount}
            soundEnabled={soundAlarmEnabled}
            soundBlocked={alarmSound.blocked}
            t={t}
            onEnableSound={() => {
              handleSetSoundAlarmEnabled(true);
              void alarmSound.enable();
            }}
          />

          <DeviceSummary
            location={deviceLocation}
            t={t}
            locale={locale}
            onOpenManualLocation={handleOpenManualLocation}
            manualLocationPickMode={manualLocationPickMode}
            onManualLocationPickToggle={handleToggleManualLocationPickMode}
          />

          <div className={tab === "fpv" ? "screen-list screen-list--fpv" : "screen-list"}>
            {tab === "positions" ? (
              visiblePositions.length ? (
                visiblePositions.map((target) => {
                  const whitelisted = isSerialWhitelisted(target.serial, userSettings.whitelist);
                  const alert = targetTriggersAlarm(target, whitelisted, activeWarningZone);
                  return (
                    <PositionCard
                      key={target.id}
                      target={target}
                      whitelisted={whitelisted}
                      alert={alert}
                      whitelistBusy={whitelistBusySerial === normalizeWhitelistSerial(target.serial)}
                      selected={target.id === selectedPositionId}
                      t={t}
                      locale={locale}
                      now={now}
                      expireSeconds={positionExpireSeconds}
                      onSelect={() => handleSelectPosition(target)}
                      onOpenNavigationQRCode={handleOpenNavigationQRCode}
                      onToggleWhitelist={handleTogglePositionWhitelist}
                    />
                  );
                })
              ) : (
                <EmptyState icon={<Satellite aria-hidden="true" />} text={t.emptyPositions} />
              )
            ) : visibleFPV.length ? (
              <FPVTable
                targets={visibleFPV}
                t={t}
                now={now}
                videoAvailable={Boolean(fpvVideoPlaybackURL)}
                videoBusy={fpvVideoBusy}
                videoOpeningId={fpvVideoOpeningId}
                onViewVideo={handleOpenFPVVideo}
              />
            ) : (
              <EmptyState icon={<Signal aria-hidden="true" />} text={t.emptyFPV} />
            )}
          </div>

          <div className="screen-tabs" role="tablist">
            <button
              type="button"
              className={tab === "positions" ? "screen-tab screen-tab--active" : "screen-tab"}
              role="tab"
              aria-selected={tab === "positions"}
              onClick={() => setTab("positions")}
            >
              <TabStatusDot status={status?.position} />
              <MapPin className="screen-tab__icon" aria-hidden="true" />
              <span>{t.positions}</span>
              <strong>{visiblePositions.length}</strong>
            </button>
            <button
              type="button"
              className={tab === "fpv" ? "screen-tab screen-tab--active" : "screen-tab"}
              role="tab"
              aria-selected={tab === "fpv"}
              onClick={() => setTab("fpv")}
            >
              <TabStatusDot status={status?.fpv} />
              <Radio className="screen-tab__icon" aria-hidden="true" />
              <span>{t.fpv}</span>
              <strong>{fpv.length}</strong>
            </button>
          </div>
        </div>
      </aside>
      </>
      ) : (
        <ManagementView
            view={view}
            t={t}
            locale={locale}
            userSettings={userSettings}
            status={status}
            defaultScreenTitle={t.title}
            onStatusChange={setStatus}
            onSaveUserSettings={saveUserSettings}
          />
      )}

      <footer className="screen-footer" aria-hidden="true">
        <span className="screen-footer-bg" dangerouslySetInnerHTML={{ __html: footerBg }} />
      </footer>

      <NavigationQRCodeModal
        state={navigationQRCode}
        loading={navigationQRCodeLoading}
        error={navigationQRCodeError}
        t={t}
        onClose={handleCloseNavigationQRCode}
      />
      <ManualDeviceLocationModal
        open={manualLocationOpen}
        draft={manualLocationDraft}
        saving={manualLocationSaving}
        error={manualLocationError}
        canClear={deviceLocation?.source === "manual"}
        t={t}
        onDraftChange={handleManualLocationDraftChange}
        onSave={handleSaveManualLocation}
        onClear={handleClearManualLocation}
        onClose={handleCloseManualLocation}
      />
      <FPVVideoModal
        target={fpvVideoTarget}
        playbackURL={fpvVideoPlaybackURL}
        sessionToken={fpvVideoSessionToken}
        closing={fpvVideoClosing}
        t={t}
        locale={locale}
        onClose={handleCloseFPVVideo}
      />
      <VirtualKeyboard locale={locale} localeOptions={virtualKeyboardLocaleOptions} labels={t} />
    </main>
  );
}

function ScreenMap({
  positions,
  selectedPosition,
  deviceLocation,
  whitelist,
  warningZone,
  warningZoneEnabled = false,
  manualLocationPickMode = false,
  onSelectPosition,
  onManualLocationPick,
  onWarningZoneToggle,
  t,
  locale,
  showLayerControl = true,
}: {
  positions: ScreenPositionTarget[];
  selectedPosition: ScreenPositionTarget | null;
  deviceLocation: ScreenDeviceLocationResponse | null;
  whitelist?: WhitelistItem[];
  warningZone?: WarningZone | null;
  warningZoneEnabled?: boolean;
  manualLocationPickMode?: boolean;
  onSelectPosition: (target: ScreenPositionTarget) => void;
  onManualLocationPick?: (point: GeoPoint) => void;
  onWarningZoneToggle?: (enabled: boolean) => void | Promise<void>;
  t: Record<string, string>;
  locale: Locale;
  showLayerControl?: boolean;
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<L.Map | null>(null);
  const layerRef = useRef<L.LayerGroup | null>(null);
  const fitOnceRef = useRef(false);
  const dataRef = useRef<ScreenMapData>({ deviceLocation, positions, warningZone: warningZone ?? null });
  const onWarningZoneToggleRef = useRef(onWarningZoneToggle);
  const onManualLocationPickRef = useRef(onManualLocationPick);
  const warningZoneEnabledRef = useRef(warningZoneEnabled);
  const manualLocationPickModeRef = useRef(manualLocationPickMode);
  const warningZoneEditable = Boolean(onWarningZoneToggle);
  const manualLocationPickable = Boolean(onManualLocationPick);
  const layerLabels = useMemo(() => {
    return Object.fromEntries(referenceMapLayers.map((key) => [key, t[key] ?? key])) as Record<ReferenceMapLayer, string>;
  }, [t]);
  const layerLabelsRef = useRef(layerLabels);

  useEffect(() => {
    dataRef.current = { deviceLocation, positions, warningZone: warningZone ?? null };
  }, [deviceLocation, positions, warningZone]);

  useEffect(() => {
    onWarningZoneToggleRef.current = onWarningZoneToggle;
  }, [onWarningZoneToggle]);

  useEffect(() => {
    onManualLocationPickRef.current = onManualLocationPick;
  }, [onManualLocationPick]);

  useEffect(() => {
    warningZoneEnabledRef.current = warningZoneEnabled;
    const container = containerRef.current;
    const button = container?.querySelector<HTMLAnchorElement>(".warning-zone-button");
    if (!button) {
      return;
    }
    button.classList.toggle("warning-zone-button--active", warningZoneEnabled);
    button.title = warningZoneEnabled ? t.disableWarningZone : t.enableWarningZone;
    button.setAttribute("aria-label", button.title);
    button.setAttribute("aria-pressed", String(warningZoneEnabled));
  }, [t.disableWarningZone, t.enableWarningZone, warningZoneEnabled]);

  useEffect(() => {
    manualLocationPickModeRef.current = manualLocationPickMode;
    const container = containerRef.current;
    container?.classList.toggle("screen-map--picking-manual-location", manualLocationPickMode);
    return () => {
      container?.classList.remove("screen-map--picking-manual-location");
    };
  }, [manualLocationPickMode]);

  useEffect(() => {
    const map = mapRef.current;
    if (!map || manualLocationPickable) {
      return;
    }
    if (manualLocationPickModeRef.current) {
      manualLocationPickModeRef.current = false;
      map.getContainer().classList.remove("screen-map--picking-manual-location");
    }
  }, [manualLocationPickable]);

  useEffect(() => {
    layerLabelsRef.current = layerLabels;
  }, [layerLabels]);

  const fitMap = useCallback(() => {
    const map = mapRef.current;
    if (!map) {
      return;
    }
    const data = dataRef.current;
    fitBounds(map, collectMapPoints(data.deviceLocation, data.positions, data.warningZone));
  }, []);

  useEffect(() => {
    const container = containerRef.current;
    if (!container || mapRef.current) {
      return;
    }

    const map = L.map(container, {
      center: referenceMapCenter,
      zoom: referenceMapZoom,
      zoomControl: false,
      attributionControl: false,
    });

    map.createPane("screenTrajectories");
    map.createPane("screenMarkers");
    map.createPane("screenSelectedMarkers");
    map.getPane("screenTrajectories")!.style.zIndex = "580";
    map.getPane("screenMarkers")!.style.zIndex = "610";
    map.getPane("screenSelectedMarkers")!.style.zIndex = "660";

    const availableMapLayers = referenceMapLayers;
    const baseLayers = buildBaseLayers();
    const activeLayer = resolveActiveMapLayer(
      getStoredMapLayer(),
      availableMapLayers,
      referenceDefaultMapLayerForLocale(locale),
    );
    baseLayers[activeLayer].addTo(map);

    map.addControl(
      createDrawControlButtonGroup([
        {
          title: t.center,
          contentType: "image",
          text: centerPointIcon,
          className: "center-point-button",
          onClick: () => {
            const data = dataRef.current;
            fitBounds(map, collectMapPoints(data.deviceLocation, data.positions, data.warningZone));
          },
        },
        ...(warningZoneEditable ? [
          {
            title: warningZoneEnabledRef.current ? t.disableWarningZone : t.enableWarningZone,
            contentType: "html" as const,
            text: warningZoneControlIcon,
            className: warningZoneEnabledRef.current ? "warning-zone-button warning-zone-button--active" : "warning-zone-button",
            onClick: () => {
              void onWarningZoneToggleRef.current?.(!warningZoneEnabledRef.current);
            },
          },
        ] : []),
      ]),
    );
    L.control.zoom({
      position: "topleft",
      zoomInTitle: "+",
      zoomOutTitle: "-",
    }).addTo(map);

    if (showLayerControl) {
      const labels = layerLabelsRef.current;
      const layersControl = new L.Control.Layers(
        Object.fromEntries(availableMapLayers.map((key) => [labels[key], baseLayers[key]])),
        {},
        {
          position: "topleft",
        },
      );
      map.addControl(layersControl);
      map.on("baselayerchange", (event: L.LayersControlEvent) => {
        const nextLayer = availableMapLayers.find((key) => layerLabelsRef.current[key] === event.name);
        if (nextLayer) {
          persistMapLayer(nextLayer);
        }
      });
    }

    map.on("click", (event: L.LeafletMouseEvent) => {
      if (!manualLocationPickModeRef.current) {
        return;
      }
      onManualLocationPickRef.current?.({
        latitude: Number(event.latlng.lat.toFixed(6)),
        longitude: Number(event.latlng.lng.toFixed(6)),
      });
    });

    mapRef.current = map;
    layerRef.current = L.layerGroup().addTo(map);
    const timer = window.setTimeout(() => {
      map.invalidateSize();
      fitMap();
    }, 0);

    return () => {
      window.clearTimeout(timer);
      map.remove();
      mapRef.current = null;
      layerRef.current = null;
      fitOnceRef.current = false;
    };
  }, [fitMap, locale, showLayerControl, t.center, t.disableWarningZone, t.enableWarningZone, warningZoneEditable]);

  useEffect(() => {
    const map = mapRef.current;
    const layer = layerRef.current;
    if (!map || !layer) {
      return;
    }

    layer.clearLayers();
    if (warningZone) {
      renderWarningZone(layer, warningZone, locale, t);
    }
    renderDeviceMarker(layer, deviceLocation, t);
    positions.forEach((target) => {
      renderTrajectory(layer, target, "pilot", selectedPosition?.id === target.id, onSelectPosition, t);
      renderTrajectory(layer, target, "drone", selectedPosition?.id === target.id, onSelectPosition, t);
      const whitelisted = isSerialWhitelisted(target.serial, whitelist);
      const alert = targetTriggersAlarm(target, whitelisted, warningZone ?? null);
      renderTargetMarker(layer, target, "pilot", selectedPosition?.id === target.id, alert, onSelectPosition, t);
      renderTargetMarker(layer, target, "drone", selectedPosition?.id === target.id, alert, onSelectPosition, t);
      renderHomeMarker(layer, target, selectedPosition?.id === target.id, onSelectPosition, t);
    });

    const points = collectMapPoints(deviceLocation, positions, warningZone);
    if (!fitOnceRef.current && points.length) {
      fitBounds(map, points);
      fitOnceRef.current = true;
    }
  }, [deviceLocation, locale, onSelectPosition, positions, selectedPosition?.id, t, warningZone, whitelist]);

  useEffect(() => {
    const map = mapRef.current;
    const point = selectedPosition ? firstMapPoint(selectedPosition) : null;
    if (map && point && validMapPoint(point)) {
      map.setView([point.latitude, point.longitude], Math.max(map.getZoom(), 14), { animate: true });
    }
  }, [selectedPosition]);

  return (
    <div className="screen-map-shell">
      <div ref={containerRef} className="screen-map dark" aria-label="map" />
      {manualLocationPickMode ? <div className="screen-map-manual-pick-hint">{t.manualLocationPickHint}</div> : null}
      {warningZoneEnabled && !warningZone ? <div className="screen-map-warning-zone-hint">{t.warningZoneNoDeviceLocation}</div> : null}
    </div>
  );
}

function getOfflineTileBase() {
  if (typeof window === "undefined") {
    return "";
  }
  const configuredBase = import.meta.env.VITE_BASE_PATH?.trim();
  if (configuredBase) {
    return configuredBase.replace(/\/+$/, "");
  }
  return "";
}

function buildBaseLayers(): Record<ReferenceMapLayer, L.TileLayer> {
  return {
    "leaflet.map.gaodeMap": L.tileLayer(
      "https://webrd04.is.autonavi.com/appmaptile?lang=zh_cn&size=1&scale=1&style=7&x={x}&y={y}&z={z}",
      { coordFunction: "gps84ToGcj02" },
    ),
    "leaflet.map.gaodeSatellite": L.tileLayer("https://webst01.is.autonavi.com/appmaptile?style=6&x={x}&y={y}&z={z}", {
      coordFunction: "gps84ToGcj02",
      minZoom: 3,
      maxZoom: 16,
    }),
    "leaflet.map.googleMap": L.tileLayer("https://mt1.google.com/vt/lyrs=m&x={x}&y={y}&z={z}", {
      coordFunction: "gps84ToGcj02",
      maxZoom: 22,
    }),
    "leaflet.map.googleSatellite": L.tileLayer("https://mt1.google.com/vt/lyrs=s&x={x}&y={y}&z={z}", {
      maxZoom: 21,
    }),
    "leaflet.map.offlineMap": L.tileLayer(`${getOfflineTileBase()}/map/dt/{z}/{x}/{y}.jpg`),
  };
}

function referenceDefaultMapLayerForLocale(locale?: string): ReferenceMapLayer {
  return locale?.startsWith("en") ? "leaflet.map.googleSatellite" : referenceDefaultMapLayer;
}

function parseStoredMapLayer(raw: string | null): ReferenceMapLayer | null {
  if (!raw) {
    return null;
  }
  if (referenceMapLayers.includes(raw as ReferenceMapLayer)) {
    return raw as ReferenceMapLayer;
  }

  try {
    const parsed = JSON.parse(raw) as unknown;
    if (typeof parsed === "string" && referenceMapLayers.includes(parsed as ReferenceMapLayer)) {
      return parsed as ReferenceMapLayer;
    }
    if (parsed && typeof parsed === "object" && "mapLayer" in parsed) {
      const layer = (parsed as { mapLayer?: unknown }).mapLayer;
      if (typeof layer === "string" && referenceMapLayers.includes(layer as ReferenceMapLayer)) {
        return layer as ReferenceMapLayer;
      }
    }
  } catch {
    // Ignore malformed storage values.
  }

  return null;
}

function getStoredMapLayer(): ReferenceMapLayer | null {
  if (typeof window === "undefined") {
    return null;
  }

  for (const key of [referenceMapLayerStorageKey, referenceLegacyMapLayerStorageKey]) {
    try {
      const layer = parseStoredMapLayer(window.localStorage.getItem(key));
      if (layer) {
        return layer;
      }
    } catch {
      // Ignore storage errors and continue to the next key.
    }
  }

  return null;
}

function persistMapLayer(layer: ReferenceMapLayer) {
  if (typeof window === "undefined") {
    return;
  }

  try {
    window.localStorage.setItem(referenceMapLayerStorageKey, JSON.stringify({ mapLayer: layer }));
    window.localStorage.removeItem(referenceLegacyMapLayerStorageKey);
  } catch {
    // Ignore storage errors.
  }
}

function resolveActiveMapLayer(
  storedLayer: ReferenceMapLayer | null,
  availableMapLayers: ReferenceMapLayer[],
  defaultMapLayer: ReferenceMapLayer,
) {
  if (storedLayer && availableMapLayers.includes(storedLayer)) {
    return storedLayer;
  }
  if (availableMapLayers.includes(defaultMapLayer)) {
    return defaultMapLayer;
  }
  return availableMapLayers[0] ?? referenceDefaultMapLayer;
}

function ScreenMapLegend({ t }: { t: Record<string, string> }) {
  const items = [
    { id: "device", label: t.deviceLocation, kind: "marker" as const, iconUrl: detectionDeviceIconOnlineUrl },
    { id: "drone", label: t.whitelistDrone, kind: "marker" as const, iconUrl: uavIconUrl, iconClassName: "screen-legend-panel__icon--whitelist" },
    { id: "drone-alert", label: t.unwhitelistedDrone, kind: "marker" as const, iconUrl: uavBlackFlyIconUrl, iconClassName: "screen-legend-panel__icon--alert" },
    { id: "pilot", label: t.whitelistPilot, kind: "marker" as const, iconUrl: remoteControlIconUrl, iconClassName: "screen-legend-panel__icon--whitelist" },
    { id: "pilot-alert", label: t.unwhitelistedPilot, kind: "marker" as const, iconUrl: remoteControlBlackFlyIconUrl, iconClassName: "screen-legend-panel__icon--alert" },
    { id: "drone-track", label: t.trajectory, kind: "line" as const, color: droneTrackColor },
    { id: "pilot-track", label: t.pilotTrajectory, kind: "line" as const, color: pilotTrackColor },
    { id: "warning-zone", label: t.warningZone, kind: "circle" as const, color: "#f97316" },
  ];

  return (
    <details className="screen-legend-toggle">
      <summary className="screen-legend-trigger" aria-label={t.mapLegend} title={t.mapLegend}>
        <Info size={13} strokeWidth={2.4} aria-hidden="true" />
        <span>{t.mapLegend}</span>
      </summary>
      <div className="screen-legend-panel" role="note" aria-label={t.mapLegend}>
        <strong className="screen-legend-panel__title">{t.mapLegend}</strong>
        <div className="screen-legend-panel__items">
          {items.map((item) => (
            <div key={item.id} className={item.kind === "line" || item.kind === "circle" ? "screen-legend-panel__item screen-legend-panel__item--line" : "screen-legend-panel__item"}>
              {item.kind === "marker" ? (
                <img className={`screen-legend-panel__icon ${item.iconClassName ?? ""}`} src={item.iconUrl} alt="" aria-hidden="true" />
              ) : item.kind === "circle" ? (
                <span className="screen-legend-panel__circle" aria-hidden="true" style={{ borderColor: item.color }} />
              ) : (
                <span className="screen-legend-panel__line" aria-hidden="true" style={{ backgroundColor: item.color }} />
              )}
              <span>{item.label}</span>
            </div>
          ))}
        </div>
      </div>
    </details>
  );
}

function ViewSwitch({
  view,
  t,
  onViewChange,
}: {
  view: View;
  t: Record<string, string>;
  onViewChange: (view: View) => void;
}) {
  const items: Array<{ id: View; label: string; icon: ReactNode; hidden?: boolean }> = [
    { id: "screen", label: t.screenView, icon: <Satellite size={14} aria-hidden="true" /> },
    { id: "intrusions", label: t.intrusionsView, icon: <ListFilter size={14} aria-hidden="true" /> },
    { id: "fpvRecords", label: t.fpvRecordsView, icon: <FileVideo size={14} aria-hidden="true" /> },
    { id: "interferenceReports", label: t.interferenceReportsView, icon: <Zap size={14} aria-hidden="true" /> },
    { id: "whitelist", label: t.whitelistView, icon: <ShieldCheck size={14} aria-hidden="true" /> },
    { id: "offlineMap", label: t.offlineMapView, icon: <HardDriveUpload size={14} aria-hidden="true" /> },
    { id: "settings", label: t.settingsView, icon: <Settings size={14} aria-hidden="true" /> },
  ];
  return (
    <nav className="screen-view-switch" aria-label="view">
      {items.filter((item) => !item.hidden).map((item) => (
        <button
          key={item.id}
          className={view === item.id ? "screen-view-switch__item screen-view-switch__item--active" : "screen-view-switch__item"}
          type="button"
          title={item.label}
          aria-label={item.label}
          aria-current={view === item.id ? "page" : undefined}
          onClick={() => onViewChange(item.id)}
        >
          {item.icon}
          <span>{item.label}</span>
        </button>
      ))}
    </nav>
  );
}

function ScreenAlarmBanner({
  activeCount,
  soundEnabled,
  soundBlocked,
  t,
  onEnableSound,
}: {
  activeCount: number;
  soundEnabled: boolean;
  soundBlocked: boolean;
  t: Record<string, string>;
  onEnableSound: () => void;
}) {
  if (activeCount <= 0 && !soundBlocked) {
    return null;
  }

  return (
    <div className={activeCount > 0 ? "screen-alarm-banner screen-alarm-banner--active" : "screen-alarm-banner"}>
      <span className="screen-alarm-banner__icon">
        <BellRing size={14} aria-hidden="true" />
      </span>
      <span className="screen-alarm-banner__text">
        <strong>{activeCount > 0 ? `${t.activeAlarmTargets}: ${activeCount}` : t.soundAlarm}</strong>
        <em>{soundBlocked ? t.soundAlarmBlocked : soundEnabled ? t.soundAlarmOn : t.soundAlarmOff}</em>
      </span>
      {soundBlocked ? (
        <button type="button" onClick={onEnableSound}>
          <Volume2 size={13} aria-hidden="true" />
          <span>{t.enableSoundAlarm}</span>
        </button>
      ) : null}
    </div>
  );
}

function ScreenStrikePanel({
  state,
  stateSyncedAt,
  connectionStatus,
  now,
  locale,
  userSettings,
  collapsed,
  t,
  onStateChange,
  onToggleCollapsed,
}: {
  state: ScreenStrikeState | null;
  stateSyncedAt: number;
  connectionStatus?: TCPClientStatus;
  now: Date;
  locale: Locale;
  userSettings: UserSettings;
  collapsed: boolean;
  t: Record<string, string>;
  onStateChange: (state: ScreenStrikeState) => void;
  onToggleCollapsed: () => void;
}) {
  const [hovered, setHovered] = useState(false);
  const [selectedChannelIds, setSelectedChannelIds] = useState<string[]>([]);
  const [durationInput, setDurationInput] = useState(String(screenStrikeDefaultDurationSeconds));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const channels = state?.channels ?? [];
  const active = Boolean(state?.active);
  const activeChannelIdsKey = active ? state?.channelIds.join("|") ?? "" : "";
  const strikeChannelLabels = normalizeScreenStrikeChannelLabels(userSettings.screenStrikeChannelLabels);
  const durationNumber = Number(durationInput);
  const durationValid = Number.isFinite(durationNumber) &&
    durationNumber >= screenStrikeMinDurationSeconds &&
    durationNumber <= screenStrikeMaxDurationSeconds;
  const remainingSeconds = getStrikeRemainingSeconds(state, now, stateSyncedAt);
  const selectedCount = active ? state?.channelIds.length ?? 0 : selectedChannelIds.length;
  const startDisabled = busy || active || selectedChannelIds.length === 0 || !durationValid;
  const stopDisabled = busy || !active;

  useEffect(() => {
    if (active && state?.channelIds?.length) {
      setSelectedChannelIds(state.channelIds);
    }
  }, [active, activeChannelIdsKey, state?.channelIds]);

  const toggleChannel = (id: string) => {
    setSelectedChannelIds((items) => items.includes(id) ? items.filter((item) => item !== id) : [...items, id]);
    setError("");
  };

  const submit = async () => {
    setError("");
    setBusy(true);
    try {
      if (active) {
        const response = await updateScreenStrike({ enabled: false, channelIds: [], durationSeconds: 0 });
        onStateChange(response.state);
        return;
      }
      if (selectedChannelIds.length === 0) {
        setError(t.strikeSelectRequired);
        return;
      }
      if (!durationValid) {
        setError(t.strikeDurationInvalid);
        return;
      }
      const durationSeconds = clampStrikeDuration(durationNumber);
      setDurationInput(String(durationSeconds));
      const response = await updateScreenStrike({
        enabled: true,
        channelIds: selectedChannelIds,
        durationSeconds,
      });
      onStateChange(response.state);
    } catch (err) {
      setError(err instanceof Error ? err.message : t.saveFailed);
    } finally {
      setBusy(false);
    }
  };

  return (
    <aside
      className={[
        "screen-strike-panel",
        collapsed ? "screen-strike-panel--collapsed" : "",
        collapsed || hovered ? "screen-strike-panel--show-toggle" : "",
      ].filter(Boolean).join(" ")}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <button
        className="screen-side-toggle screen-side-toggle--left"
        type="button"
        aria-label={collapsed ? t.expandPanel : t.collapsePanel}
        onClick={onToggleCollapsed}
      >
        {collapsed ? <ChevronRight size={18} aria-hidden="true" /> : <ChevronLeft size={18} aria-hidden="true" />}
        <span aria-hidden="true" />
      </button>

      <div className="screen-strike-panel__inner">
        <div className="screen-strike-panel__header">
          <div className="screen-panel-title">
            <span className="screen-panel-title__icon screen-panel-title__icon--strike">
              <Zap size={15} aria-hidden="true" />
            </span>
            <span className="screen-panel-title__text">
              <em>{t.operationPanel}</em>
              <strong>{t.strike}</strong>
            </span>
          </div>
          <div className="screen-strike-panel__indicators">
            <TCPClientStatusDot status={connectionStatus} />
            <strong className={active ? "screen-strike-panel__status screen-strike-panel__status--active" : "screen-strike-panel__status"}>
              {active ? formatCountdown(remainingSeconds) : selectedCount.toLocaleString(locale)}
            </strong>
          </div>
        </div>

        <div className="screen-strike-panel__body">
          <div className="screen-strike-panel__channels" aria-label={t.strikeChannels}>
            {channels.length ? channels.map((channel, index) => {
              const selected = selectedChannelIds.includes(channel.id);
              return (
                <label
                  key={channel.id}
                  className={selected ? "screen-strike-channel screen-strike-channel--checked" : "screen-strike-channel"}
                  title={formatStrikeChannelTitle(channel, index, strikeChannelLabels)}
                >
                  <input
                    type="checkbox"
                    checked={selected}
                    disabled={active || busy || channel.reserved}
                    onChange={() => toggleChannel(channel.id)}
                  />
                  <span aria-hidden="true" />
                  <strong>{formatStrikeChannelLabel(channel, index, strikeChannelLabels)}</strong>
                </label>
              );
            }) : <EmptyState icon={<Zap aria-hidden="true" />} text={t.waiting} />}
          </div>

          <div className="screen-strike-duration">
            <span>{t.strikeDuration}</span>
            <strong>
              {durationInput}
              <em>{t.seconds}</em>
            </strong>
          </div>

          <div className="screen-strike-duration-presets" role="radiogroup" aria-label={t.strikeDuration}>
            {screenStrikeDurationPresets.map((duration) => {
              const selected = durationInput === String(duration);
              return (
                <button
                  key={duration}
                  className={selected ? "screen-strike-duration-preset screen-strike-duration-preset--active" : "screen-strike-duration-preset"}
                  type="button"
                  role="radio"
                  aria-checked={selected}
                  disabled={active || busy}
                  onClick={() => {
                    setDurationInput(String(duration));
                    setError("");
                  }}
                >
                  <span>{duration}</span>
                  <em>{t.seconds}</em>
                </button>
              );
            })}
          </div>

          <div className="screen-strike-panel__footer">
            <button
              className={active ? "screen-strike-action screen-strike-action--stop" : "screen-strike-action"}
              type="button"
              disabled={active ? stopDisabled : startDisabled}
              onClick={() => void submit()}
            >
              {busy ? (
                <Loader2 className="app-spinner" size={14} aria-hidden="true" />
              ) : active ? (
                <Square size={14} aria-hidden="true" />
              ) : (
                <Zap size={15} aria-hidden="true" />
              )}
              <span>{active ? t.stopStrike : t.startStrike}</span>
            </button>
            <span className="screen-strike-panel__remaining">
              {t.strikeRemaining}: <strong>{formatCountdown(remainingSeconds)}</strong>
            </span>
          </div>

          {error ? <p className="screen-strike-panel__error">{error}</p> : null}
        </div>
      </div>
    </aside>
  );
}

function ManagementView({
  view,
  t,
  locale,
  userSettings,
  status,
  defaultScreenTitle,
  onStatusChange,
  onSaveUserSettings,
}: {
  view: Exclude<View, "screen">;
  t: Record<string, string>;
  locale: Locale;
  userSettings: UserSettings;
  status: ScreenRuntimeStatus | null;
  defaultScreenTitle: string;
  onStatusChange: (status: ScreenRuntimeStatus) => void;
  onSaveUserSettings: (settings: UserSettings) => Promise<UserSettings>;
}) {
  return (
    <section className={view === "settings" ? "screen-management-panel screen-management-panel--settings" : "screen-management-panel"}>
      {view === "intrusions" ? (
        <IntrusionsManagement t={t} locale={locale} userSettings={userSettings} onSaveUserSettings={onSaveUserSettings} />
      ) : view === "fpvRecords" ? (
        <FPVVideoRecordsManagement t={t} locale={locale} />
      ) : view === "interferenceReports" ? (
        <InterferenceReportsManagement t={t} locale={locale} userSettings={userSettings} />
      ) : view === "settings" ? (
        <ScreenSettingsManagement
          t={t}
          userSettings={userSettings}
          status={status}
          defaultScreenTitle={defaultScreenTitle}
          onStatusChange={onStatusChange}
          onSaveUserSettings={onSaveUserSettings}
        />
      ) : view === "offlineMap" ? (
        <OfflineMapManagement t={t} locale={locale} />
      ) : (
        <WhitelistManagement t={t} locale={locale} userSettings={userSettings} onSaveUserSettings={onSaveUserSettings} />
      )}
    </section>
  );
}

function OfflineMapManagement({ t, locale }: { t: Record<string, string>; locale: Locale }) {
  const [status, setStatus] = useState<OfflineMapStatus | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [keepBackup, setKeepBackup] = useState(true);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setStatus(await getOfflineMapStatus());
      setMessage("");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : String(error));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const submit = async () => {
    if (!file || busy) {
      return;
    }
    setBusy(true);
    setMessage("");
    try {
      const response = await uploadOfflineMap(file, keepBackup);
      setStatus(response.map);
      setMessage(response.message || t.offlineMapUploadSuccess);
      setFile(null);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : t.offlineMapUploadFailed);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="screen-management screen-management--settings">
      <div className="screen-management__header">
        <div className="screen-panel-title">
          <span className="screen-panel-title__icon screen-panel-title__icon--target">
            <HardDriveUpload aria-hidden="true" />
          </span>
          <span className="screen-panel-title__text">
            <em>{t.offlineMapView}</em>
            <strong>{t.offlineMapTitle}</strong>
          </span>
        </div>
        <button type="button" onClick={() => void refresh()} disabled={loading || busy}>
          {loading ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <RefreshCw size={14} aria-hidden="true" />}
          <span>{t.refresh}</span>
        </button>
      </div>

      <div className="screen-settings-grid">
        <section className="screen-settings-section screen-settings-section--display">
          <header>
            <span className="screen-settings-section__icon">
              <MapPinned size={15} aria-hidden="true" />
            </span>
            <span className="screen-settings-section__heading">
              <strong>{t.offlineMapStatus}</strong>
              <span>{t.offlineMapDescription}</span>
            </span>
          </header>
          <div className="screen-info-grid">
            <InfoBlock label={t.status} value={status?.available ? t.offlineMapAvailable : t.offlineMapUnavailable} />
            <InfoBlock label={t.offlineMapTiles} value={String(status?.tileCount ?? 0)} />
            <InfoBlock label={t.offlineMapUploadedAt} value={formatFullTime(status?.uploadedAt, locale)} />
            <InfoBlock label={t.offlineMapSourceFile} value={status?.sourceFile || "-"} />
            <InfoBlock label={t.offlineMapPath} value={status?.path || "-"} />
          </div>
        </section>

        <section className="screen-settings-section screen-settings-section--tcp">
          <header>
            <span className="screen-settings-section__icon">
              <HardDriveUpload size={15} aria-hidden="true" />
            </span>
            <span className="screen-settings-section__heading">
              <strong>{t.offlineMapUpload}</strong>
              <span>{file ? `${file.name} · ${formatFileSize(file.size, locale)}` : t.offlineMapSelectFile}</span>
            </span>
          </header>
          <div className="screen-settings-form-grid">
            <label>
              <span>{t.offlineMapFile}</span>
              <input
                type="file"
                accept=".zip"
                disabled={busy}
                onChange={(event) => setFile(event.currentTarget.files?.[0] ?? null)}
              />
            </label>
            <label className="screen-settings-toggle-row">
              <span>{t.offlineMapKeepBackup}</span>
              <input type="checkbox" checked={keepBackup} disabled={busy} onChange={(event) => setKeepBackup(event.target.checked)} />
            </label>
            <div className="screen-settings-preview">
              <span>{t.fileSize}</span>
              <strong>{file ? formatFileSize(file.size, locale) : "-"}</strong>
            </div>
          </div>
        </section>
      </div>

      {message || status?.message ? <div className="screen-management__banner">{message || status?.message}</div> : null}

      <div className="screen-management__footer screen-settings-actions">
        <button type="button" disabled={busy || !file} onClick={() => void submit()}>
          {busy ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <Check size={14} aria-hidden="true" />}
          <span>{t.offlineMapUpload}</span>
        </button>
      </div>
    </div>
  );
}

function InfoBlock({ label, value, children }: { label: string; value?: string; children?: ReactNode }) {
  return (
    <div className="screen-info-block">
      <span>{label}</span>
      <strong title={value}>{children ?? value ?? "-"}</strong>
    </div>
  );
}

function ScreenSettingsManagement({
  t,
  userSettings,
  status,
  defaultScreenTitle,
  onStatusChange,
  onSaveUserSettings,
}: {
  t: Record<string, string>;
  userSettings: UserSettings;
  status: ScreenRuntimeStatus | null;
  defaultScreenTitle: string;
  onStatusChange: (status: ScreenRuntimeStatus) => void;
  onSaveUserSettings: (settings: UserSettings) => Promise<UserSettings>;
}) {
  const savedTitle = userSettings.screenTitle?.trim() ?? "";
  const savedExpireSeconds = resolvePositionExpireSeconds(userSettings.positionExpireSeconds);
  const savedPositionTCPPort = resolveTCPPort(userSettings.positionTCPPort, status?.position?.port ?? 10007);
  const savedFPVTCPPort = resolveTCPPort(userSettings.fpvTCPPort, status?.fpv?.port ?? 10005);
  const savedStrikeLabels = normalizeScreenStrikeChannelLabels(userSettings.screenStrikeChannelLabels);
  const savedWarningZoneEnabled = Boolean(userSettings.warningZoneEnabled);
  const savedWarningZoneRadius = resolveWarningZoneRadiusMeters(userSettings);
  const [titleDraft, setTitleDraft] = useState(savedTitle);
  const [expireDraft, setExpireDraft] = useState(String(savedExpireSeconds));
  const [positionTCPPortDraft, setPositionTCPPortDraft] = useState(String(savedPositionTCPPort));
  const [fpvTCPPortDraft, setFPVTCPPortDraft] = useState(String(savedFPVTCPPort));
  const [warningZoneEnabledDraft, setWarningZoneEnabledDraft] = useState(savedWarningZoneEnabled);
  const [warningZoneRadiusDraft, setWarningZoneRadiusDraft] = useState(String(savedWarningZoneRadius));
  const [strikeLabelDrafts, setStrikeLabelDrafts] = useState(savedStrikeLabels);
  const [saving, setSaving] = useState(false);
  const [banner, setBanner] = useState("");

  useEffect(() => {
    setTitleDraft(savedTitle);
  }, [savedTitle]);

  useEffect(() => {
    setExpireDraft(String(savedExpireSeconds));
  }, [savedExpireSeconds]);

  useEffect(() => {
    setPositionTCPPortDraft(String(savedPositionTCPPort));
  }, [savedPositionTCPPort]);

  useEffect(() => {
    setFPVTCPPortDraft(String(savedFPVTCPPort));
  }, [savedFPVTCPPort]);

  useEffect(() => {
    setWarningZoneEnabledDraft(savedWarningZoneEnabled);
  }, [savedWarningZoneEnabled]);

  useEffect(() => {
    setWarningZoneRadiusDraft(String(savedWarningZoneRadius));
  }, [savedWarningZoneRadius]);

  useEffect(() => {
    setStrikeLabelDrafts(savedStrikeLabels);
  }, [savedStrikeLabels.join("|")]);

  const normalizedTitle = titleDraft.trim();
  const normalizedStrikeLabels = normalizeScreenStrikeChannelLabels(strikeLabelDrafts);
  const expireSeconds = Number(expireDraft);
  const expireValid = Number.isInteger(expireSeconds) &&
    expireSeconds >= minPositionExpireSeconds &&
    expireSeconds <= maxPositionExpireSeconds;
  const positionTCPPort = Number(positionTCPPortDraft);
  const fpvTCPPort = Number(fpvTCPPortDraft);
  const tcpPortsValid = validTCPPort(positionTCPPort) && validTCPPort(fpvTCPPort) && positionTCPPort !== fpvTCPPort;
  const warningZoneRadius = Number(warningZoneRadiusDraft);
  const warningZoneRadiusValid = Number.isInteger(warningZoneRadius) &&
    warningZoneRadius >= minWarningZoneRadiusMeters &&
    warningZoneRadius <= maxWarningZoneRadiusMeters;
  const strikeLabelsChanged = normalizedStrikeLabels.join("|") !== savedStrikeLabels.join("|");
  const changed = normalizedTitle !== savedTitle ||
    (expireValid && expireSeconds !== savedExpireSeconds) ||
    (tcpPortsValid && (positionTCPPort !== savedPositionTCPPort || fpvTCPPort !== savedFPVTCPPort)) ||
    warningZoneEnabledDraft !== savedWarningZoneEnabled ||
    (warningZoneRadiusValid && warningZoneRadius !== savedWarningZoneRadius) ||
    strikeLabelsChanged;

  const saveSettings = async () => {
    if (!expireValid) {
      setBanner(t.positionExpireInvalid);
      return;
    }
    if (!tcpPortsValid) {
      setBanner(t.tcpPortInvalid);
      return;
    }
    if (!warningZoneRadiusValid) {
      setBanner(t.warningZoneRadiusInvalid);
      return;
    }
    setSaving(true);
    setBanner("");
    try {
      const nextSettings: UserSettings = {
        screenTitle: normalizedTitle,
        positionExpireSeconds: expireSeconds,
        warningZoneEnabled: warningZoneEnabledDraft,
        warningZoneRadiusMeters: warningZoneRadius,
        screenStrikeChannelLabels: normalizedStrikeLabels,
      };
      await onSaveUserSettings(nextSettings);
      if (positionTCPPort !== savedPositionTCPPort || fpvTCPPort !== savedFPVTCPPort) {
        const nextStatus = await updateScreenTCPPorts({
          positionTCPPort,
          fpvTCPPort,
        });
        onStatusChange(nextStatus);
      }
      setBanner(t.settingsSaved);
    } catch (error) {
      setBanner(error instanceof Error ? error.message : t.saveFailed);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className={banner ? "screen-management screen-management--settings screen-management--with-banner" : "screen-management screen-management--settings"}>
      <div className="screen-management__header">
        <div className="screen-panel-title">
          <span className="screen-panel-title__icon screen-panel-title__icon--target">
            <Settings aria-hidden="true" />
          </span>
          <span className="screen-panel-title__text">
            <em>{t.settingsView}</em>
            <strong>{t.settingsTitle}</strong>
          </span>
        </div>
      </div>

      <div className="screen-settings-grid">
        <section className="screen-settings-section screen-settings-section--display">
          <header>
            <span className="screen-settings-section__icon">
              <Maximize2 size={15} aria-hidden="true" />
            </span>
            <span className="screen-settings-section__heading">
              <strong>{t.displaySettings}</strong>
              <span>{t.customScreenTitleHint}</span>
            </span>
          </header>
          <div className="screen-settings-form-grid">
            <label>
              <span>{t.customScreenTitle}</span>
              <input
                value={titleDraft}
                maxLength={32}
                placeholder={defaultScreenTitle}
                onChange={(event) => setTitleDraft(event.target.value)}
              />
            </label>
            <div className="screen-settings-preview">
              <span>{t.preview}</span>
              <strong title={normalizedTitle || defaultScreenTitle}>{normalizedTitle || defaultScreenTitle}</strong>
            </div>
          </div>
        </section>

        <section className="screen-settings-section screen-settings-section--warning">
          <header>
            <span className="screen-settings-section__icon">
              <ShieldPlus size={15} aria-hidden="true" />
            </span>
            <span className="screen-settings-section__heading">
              <strong>{t.warningZone}</strong>
              <span>{t.warningZoneRadiusHint}</span>
            </span>
          </header>
          <div className="screen-settings-form-grid">
            <label className="screen-settings-toggle-row">
              <span>{warningZoneEnabledDraft ? t.disableWarningZone : t.enableWarningZone}</span>
              <input
                type="checkbox"
                checked={warningZoneEnabledDraft}
                onChange={(event) => setWarningZoneEnabledDraft(event.target.checked)}
              />
            </label>
            <label>
              <span>{t.warningZoneRadius}</span>
              <input
                value={warningZoneRadiusDraft}
                type="text"
                inputMode="numeric"
                data-keyboard="digits"
                pattern="[0-9]*"
                onChange={(event) => setWarningZoneRadiusDraft(event.target.value.replace(/\D/g, "").slice(0, 5))}
              />
            </label>
            <div className="screen-settings-preview">
              <span>{t.savedValue}</span>
              <strong>{formatMeters(savedWarningZoneRadius, "zh-CN", t)}</strong>
            </div>
          </div>
        </section>

        <section className="screen-settings-section screen-settings-section--tcp">
          <header>
            <span className="screen-settings-section__icon">
              <Radio size={15} aria-hidden="true" />
            </span>
            <span className="screen-settings-section__heading">
              <strong>{t.tcpPortSettings}</strong>
              <span>{t.tcpPortSettingsHint}</span>
            </span>
          </header>
          <div className="screen-settings-form-grid screen-settings-form-grid--ports">
            <label>
              <span>{t.positionTCPPort}</span>
              <input
                value={positionTCPPortDraft}
                type="text"
                inputMode="numeric"
                data-keyboard="digits"
                pattern="[0-9]*"
                onChange={(event) => setPositionTCPPortDraft(event.target.value.replace(/\D/g, "").slice(0, 5))}
              />
            </label>
            <label>
              <span>{t.fpvTCPPort}</span>
              <input
                value={fpvTCPPortDraft}
                type="text"
                inputMode="numeric"
                data-keyboard="digits"
                pattern="[0-9]*"
                onChange={(event) => setFPVTCPPortDraft(event.target.value.replace(/\D/g, "").slice(0, 5))}
              />
            </label>
            <div className="screen-settings-preview">
              <span>{t.savedValue}</span>
              <strong>{savedPositionTCPPort} / {savedFPVTCPPort}</strong>
            </div>
          </div>
        </section>

        <section className="screen-settings-section screen-settings-section--expire">
          <header>
            <span className="screen-settings-section__icon">
              <TimerReset size={15} aria-hidden="true" />
            </span>
            <span className="screen-settings-section__heading">
              <strong>{t.positionExpireSettings}</strong>
              <span>{t.positionExpireHint}</span>
            </span>
          </header>
          <div className="screen-settings-form-grid">
            <label>
              <span>{t.positionExpireSeconds}</span>
              <input
                value={expireDraft}
                type="text"
                inputMode="numeric"
                data-keyboard="digits"
                pattern="[0-9]*"
                onChange={(event) => setExpireDraft(event.target.value.replace(/\D/g, "").slice(0, 4))}
              />
            </label>
            <div className="screen-settings-preview">
              <span>{t.savedValue}</span>
              <strong>{savedExpireSeconds}s</strong>
            </div>
          </div>
        </section>

        <section className="screen-settings-section screen-settings-section--strike">
          <header>
            <span className="screen-settings-section__icon">
              <Zap size={15} aria-hidden="true" />
            </span>
            <span className="screen-settings-section__heading">
              <strong>{t.screenStrikeSettings}</strong>
              <span>{t.screenStrikeChannelLabelsHint}</span>
            </span>
          </header>
          <div className="screen-settings-channel-labels">
            {strikeLabelDrafts.map((value, index) => (
              <label key={index}>
                <span>{t.screenStrikeChannelLabel.replace("{index}", String(index + 1))}</span>
                <input
                  value={value}
                  maxLength={32}
                  placeholder={defaultStrikeChannelLabel(index)}
                  onChange={(event) => {
                    const next = [...strikeLabelDrafts];
                    next[index] = event.target.value;
                    setStrikeLabelDrafts(normalizeScreenStrikeChannelLabels(next));
                  }}
                />
              </label>
            ))}
          </div>
        </section>
      </div>

      {banner ? <div className="screen-management__banner">{banner}</div> : null}

      <div className="screen-management__footer screen-settings-actions">
        <button
          type="button"
          disabled={saving}
          onClick={() => {
            setTitleDraft("");
            setExpireDraft(String(defaultPositionExpireSeconds));
            setWarningZoneEnabledDraft(false);
            setWarningZoneRadiusDraft(String(defaultWarningZoneRadiusMeters));
            setStrikeLabelDrafts(defaultStrikeChannelLabels());
          }}
        >
          <RefreshCw size={14} aria-hidden="true" />
          <span>{t.restoreDefault}</span>
        </button>
        <button type="button" disabled={saving || !changed} onClick={() => void saveSettings()}>
          {saving ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <Check size={14} aria-hidden="true" />}
          <span>{t.save}</span>
        </button>
      </div>
    </div>
  );
}

function IntrusionsManagement({
  t,
  locale,
  userSettings,
  onSaveUserSettings,
}: {
  t: Record<string, string>;
  locale: Locale;
  userSettings: UserSettings;
  onSaveUserSettings: (settings: UserSettings) => Promise<UserSettings>;
}) {
  const pageSize = 50;
  const [records, setRecords] = useState<IntrusionRecord[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [nextOffset, setNextOffset] = useState(0);
  const [loading, setLoading] = useState(false);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [modelQuery, setModelQuery] = useState("");
  const [serialQuery, setSerialQuery] = useState("");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [banner, setBanner] = useState("");
  const [busySerial, setBusySerial] = useState("");
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [mapRecord, setMapRecord] = useState<IntrusionRecord | null>(null);
  const loadRequestRef = useRef(0);

  const loadRecords = useCallback(async (offset: number, append: boolean, clearBanner = true) => {
    const requestId = loadRequestRef.current + 1;
    loadRequestRef.current = requestId;
    setLoading(true);
    try {
      const response = await getIntrusions(pageSize, offset, {
        model: modelQuery,
        serial: serialQuery,
        dateFrom,
        dateTo,
      });
      if (requestId !== loadRequestRef.current) {
        return false;
      }
      setRecords((current) => append ? appendIntrusionRecords(current, response.items) : response.items);
      if (!append) {
        setSelectedIds([]);
      }
      setHasMore(Boolean(response.hasMore));
      setNextOffset(response.nextOffset ?? 0);
      if (clearBanner) {
        setBanner("");
      }
      return true;
    } catch (error) {
      if (requestId === loadRequestRef.current) {
        setBanner(error instanceof Error ? error.message : String(error));
      }
      return false;
    } finally {
      if (requestId === loadRequestRef.current) {
        setLoading(false);
      }
    }
  }, [dateFrom, dateTo, modelQuery, serialQuery]);

  useEffect(() => {
    void loadRecords(0, false);
  }, [loadRecords]);

  const visibleRecords = records;

  const selectedCount = selectedIds.length;
  const totalTrajectoryCount = useMemo(() => visibleRecords.reduce((sum, record) => (
    sum + (record.droneTrajectory?.length ?? 0) + (record.pilotTrajectory?.length ?? 0)
  ), 0), [visibleRecords]);
  const allVisibleSelected = visibleRecords.length > 0 && visibleRecords.every((record) => selectedIds.includes(record.id));

  const toggleRecordSelected = (id: string) => {
    setSelectedIds((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id]);
  };

  const toggleVisibleSelected = () => {
    setSelectedIds((current) => {
      if (allVisibleSelected) {
        return current.filter((id) => !visibleRecords.some((record) => record.id === id));
      }
      const next = new Set(current);
      visibleRecords.forEach((record) => next.add(record.id));
      return [...next];
    });
  };

  const deleteSelectedRecords = async () => {
    if (!selectedIds.length) {
      return;
    }
    setDeleteBusy(true);
    try {
      const response = await deleteIntrusions({ ids: selectedIds });
      setSelectedIds([]);
      setDeleteConfirmOpen(false);
      const reloaded = await loadRecords(0, false, false);
      if (reloaded) {
        setBanner(`${t.deletedRecords}: ${response.deleted}`);
      }
    } catch (error) {
      setBanner(error instanceof Error ? error.message : t.deleteFailed);
    } finally {
      setDeleteBusy(false);
    }
  };

  const refreshRecords = async () => {
    await loadRecords(0, false);
  };

  const exportRecords = async () => {
    setExporting(true);
    setBanner("");
    try {
      const selected = new Set(selectedIds);
      const items = records.filter((record) => selected.has(record.id));
      if (!items.length) {
        setBanner(t.exportEmpty);
        return;
      }
      const stamp = reportTimestamp();
      downloadCSV(
        reportFileName("intrusions", stamp),
        intrusionRecordsToCSV(items, t, locale),
      );
      const trajectoryRows = intrusionTrajectoryPointRows(items, t, locale);
      if (trajectoryRows.length > 0) {
        downloadCSV(
          reportFileName("intrusion-trajectories", stamp),
          intrusionTrajectoryPointsToCSV(trajectoryRows, t),
        );
      }
    } catch (error) {
      setBanner(error instanceof Error ? error.message : t.exportFailed);
    } finally {
      setExporting(false);
    }
  };

  const toggleRecordWhitelist = async (record: IntrusionRecord) => {
    const serial = record.serial?.trim() ?? "";
    const whitelisted = isSerialWhitelisted(serial, userSettings.whitelist);
    if (!whitelisted && isPendingEncryptedDJIDrone(record)) {
      setBanner(t.parsingTarget);
      return;
    }
    if (!serial) {
      setBanner(t.whitelistSerialRequired);
      return;
    }
    const key = normalizeWhitelistSerial(serial);
    setBusySerial(key);
    try {
      await onSaveUserSettings({
        whitelist: whitelisted
          ? removeWhitelistSerial(userSettings.whitelist, serial)
          : upsertWhitelistItem(userSettings.whitelist, {
            serial,
            model: record.model,
            source: record.source || "intrusion",
          }),
      });
      setBanner(whitelisted ? t.whitelistDeleted : t.whitelistSaved);
    } catch (error) {
      setBanner(error instanceof Error ? error.message : t.saveFailed);
    } finally {
      setBusySerial("");
    }
  };

  return (
    <div className={banner ? "screen-management screen-management--intrusions screen-management--with-banner" : "screen-management screen-management--intrusions"}>
      <div className="screen-management__header">
        <div className="screen-panel-title">
          <span className="screen-panel-title__icon screen-panel-title__icon--target">
            <ListFilter aria-hidden="true" />
          </span>
          <span className="screen-panel-title__text">
            <em>{t.intrusionsView}</em>
            <strong>{t.intrusionList}</strong>
          </span>
        </div>
        <div className="screen-management__actions">
          <button type="button" disabled={loading || deleteBusy || exporting} onClick={() => void refreshRecords()}>
            <RefreshCw className={loading ? "app-spinner" : undefined} size={14} aria-hidden="true" />
            <span>{t.refresh}</span>
          </button>
          <button type="button" disabled={!selectedCount || loading || deleteBusy || exporting} onClick={() => void exportRecords()}>
            {exporting ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <Download size={14} aria-hidden="true" />}
            <span>{exporting ? t.exporting : t.exportReport}</span>
          </button>
          <button type="button" disabled={!selectedCount || deleteBusy} onClick={() => setDeleteConfirmOpen(true)}>
            {deleteBusy ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <Trash2 size={14} aria-hidden="true" />}
            <span>{t.deleteSelected}</span>
          </button>
        </div>
      </div>

      <div className="screen-management__summary" aria-label={t.intrusionList}>
        <span>{t.recordCount}: {visibleRecords.length}</span>
        <span>{t.selectedCount}: {selectedCount}</span>
        <span>{t.trajectoryCount}: {totalTrajectoryCount}</span>
      </div>

      <div className="screen-management__filters" aria-label={t.filter}>
        <label>
          <Search size={13} aria-hidden="true" />
          <span>{t.modelFilter}</span>
          <input value={modelQuery} onChange={(event) => setModelQuery(event.target.value)} />
        </label>
        <label>
          <Search size={13} aria-hidden="true" />
          <span>{t.serialFilter}</span>
          <input value={serialQuery} onChange={(event) => setSerialQuery(event.target.value)} />
        </label>
        <label>
          <span>{t.dateFrom}</span>
          <input type="date" value={dateFrom} onChange={(event) => {
            const value = event.target.value;
            setDateFrom(value);
            setDateTo((current) => value && current && current < value ? value : current);
          }} />
        </label>
        <label>
          <span>{t.dateTo}</span>
          <input type="date" min={dateFrom || undefined} value={dateTo} onChange={(event) => setDateTo(event.target.value)} />
        </label>
        <button type="button" onClick={() => {
          setModelQuery("");
          setSerialQuery("");
          setDateFrom("");
          setDateTo("");
        }}>
          <X size={13} aria-hidden="true" />
          <span>{t.clearFilters}</span>
        </button>
      </div>

      {banner ? <div className="screen-management__banner">{banner}</div> : null}

      <div className="screen-management-table-wrap">
        <table className="screen-management-table screen-management-table--intrusions">
          <colgroup>
            <col className="screen-management-table__select-col" />
            <col className="screen-management-table__model-col" />
            <col className="screen-management-table__identity-col" />
            <col className="screen-management-table__frequency-col" />
            <col className="screen-management-table__signal-col" />
            <col className="screen-management-table__time-col" />
            <col className="screen-management-table__time-col" />
	            <col className="screen-management-table__duration-col" />
	            <col className="screen-management-table__coordinates-col" />
	            <col className="screen-management-table__replay-col" />
	            <col className="screen-management-table__metric-col" />
            <col className="screen-management-table__metric-col" />
            <col className="screen-management-table__metric-col" />
            <col className="screen-management-table__metric-col" />
          </colgroup>
          <thead>
            <tr>
              <th>
                <input type="checkbox" checked={allVisibleSelected} onChange={toggleVisibleSelected} aria-label={t.selectedCount} />
              </th>
              <th>{t.model}</th>
              <th>{t.serial}</th>
              <th>{t.frequency}</th>
              <th>{t.rssi}</th>
              <th>{t.firstSeen}</th>
              <th>{t.lastSeen}</th>
	              <th>{t.duration}</th>
	              <th>{t.coordinate}</th>
	              <th>{t.trajectoryReplay}</th>
	              <th>{t.pilotDistance}</th>
              <th>{t.droneDistance}</th>
              <th>{t.speed}</th>
              <th>{t.height}</th>
            </tr>
          </thead>
          <tbody>
            {visibleRecords.length ? visibleRecords.map((record) => {
              const whitelisted = isSerialWhitelisted(record.serial, userSettings.whitelist);
              const serialKey = normalizeWhitelistSerial(record.serial);
              const whitelistDisabled = (!whitelisted && isPendingEncryptedDJIDrone(record)) || !record.serial || Boolean(busySerial);
              const displayModel = resolveDisplayModel(record) || t.unknown;
              const hasMap = hasIntrusionMapData(record);
              return (
                <tr key={record.id}>
                  <td>
                    <input type="checkbox" checked={selectedIds.includes(record.id)} onChange={() => toggleRecordSelected(record.id)} aria-label={record.serial || record.id} />
                  </td>
                  <td>
                    <strong title={displayModel}>{displayModel}</strong>
                  </td>
                  <td>
                    <div className="screen-intrusion-identity">
                      <strong title={record.serial || "-"}>{record.serial || "-"}</strong>
                      <button
                        type="button"
                        disabled={whitelistDisabled}
                        className={whitelisted ? "screen-table-action screen-table-action--active" : "screen-table-action"}
                        onClick={() => void toggleRecordWhitelist(record)}
                        title={whitelisted ? t.removeFromWhitelist : t.addToWhitelist}
                      >
                        {busySerial === serialKey ? <Loader2 className="app-spinner" size={13} aria-hidden="true" /> : whitelisted ? <ShieldMinus size={13} aria-hidden="true" /> : <ShieldPlus size={13} aria-hidden="true" />}
                        <span>{whitelisted ? t.removeFromWhitelistShort : t.addToWhitelistShort}</span>
                      </button>
                    </div>
                  </td>
                  <td>{formatFrequency(record.frequency)}</td>
                  <td>{formatRSSI(record.rssi)}</td>
                  <td>{formatFullTime(record.firstSeen, locale)}</td>
                  <td>{formatFullTime(record.lastSeen, locale)}</td>
                  <td>{formatDuration(record.durationSeconds)}</td>
	                  <td>
	                    <IntrusionCoordinateCell record={record} t={t} hasMap={hasMap} />
	                  </td>
	                  <td>
	                    <IntrusionReplayCell record={record} t={t} hasMap={hasMap} onOpenMap={setMapRecord} />
	                  </td>
                  <td>{formatMeters(record.pilotDistanceM, locale, t)}</td>
                  <td>{formatMeters(record.droneDistanceM, locale, t)}</td>
                  <td>{formatSpeed(record.speed, locale, t)}</td>
                  <td>{formatMeters(record.height, locale, t)}</td>
                </tr>
              );
            }) : (
              <tr>
	                <td colSpan={14}>
                  <div className="screen-management-empty">{loading ? t.waiting : t.noIntrusions}</div>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="screen-management__footer">
        <span>{visibleRecords.length} / {records.length}</span>
        <button type="button" disabled={!hasMore || loading} onClick={() => void loadRecords(nextOffset, true)}>
          {loading ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <ChevronDown size={14} aria-hidden="true" />}
          <span>{t.loadMore}</span>
        </button>
      </div>

      {deleteConfirmOpen ? (
        <IntrusionDeleteConfirm
          count={selectedCount}
          busy={deleteBusy}
          t={t}
          onCancel={() => setDeleteConfirmOpen(false)}
          onConfirm={() => void deleteSelectedRecords()}
        />
      ) : null}

      {mapRecord ? (
        <IntrusionMapModal
          record={mapRecord}
          locale={locale}
          t={t}
          userSettings={userSettings}
          onClose={() => setMapRecord(null)}
        />
      ) : null}
    </div>
  );
}

function FPVVideoRecordsManagement({
  t,
  locale,
}: {
  t: Record<string, string>;
  locale: Locale;
}) {
  const pageSize = 50;
  const [records, setRecords] = useState<FPVVideoRecord[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [nextOffset, setNextOffset] = useState(0);
  const [loading, setLoading] = useState(false);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [signalTypeQuery, setSignalTypeQuery] = useState("");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [banner, setBanner] = useState("");
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [videoRecord, setVideoRecord] = useState<FPVVideoRecord | null>(null);
  const loadRequestRef = useRef(0);

  const loadRecords = useCallback(async (offset: number, append: boolean, clearBanner = true) => {
    const requestId = loadRequestRef.current + 1;
    loadRequestRef.current = requestId;
    setLoading(true);
    try {
      const response = await getFPVVideoRecords(pageSize, offset, {
        signalType: signalTypeQuery,
        dateFrom,
        dateTo,
      });
      if (requestId !== loadRequestRef.current) {
        return false;
      }
      setRecords((current) => append ? appendFPVVideoRecords(current, response.items) : response.items);
      if (!append) {
        setSelectedIds([]);
      }
      setHasMore(Boolean(response.hasMore));
      setNextOffset(response.nextOffset ?? 0);
      if (clearBanner) {
        setBanner("");
      }
      return true;
    } catch (error) {
      if (requestId === loadRequestRef.current) {
        setBanner(error instanceof Error ? error.message : String(error));
      }
      return false;
    } finally {
      if (requestId === loadRequestRef.current) {
        setLoading(false);
      }
    }
  }, [dateFrom, dateTo, signalTypeQuery]);

  useEffect(() => {
    void loadRecords(0, false);
  }, [loadRecords]);

  const selectedCount = selectedIds.length;
  const selectedExportableIds = useMemo(() => {
    const selected = new Set(selectedIds);
    return records
      .filter((record) => selected.has(record.id) && record.status === "ready" && Boolean(record.fileUrl))
      .map((record) => record.id);
  }, [records, selectedIds]);
  const selectedExportableCount = selectedExportableIds.length;
  const allVisibleSelected = records.length > 0 && records.every((record) => selectedIds.includes(record.id));

  const toggleRecordSelected = (id: string) => {
    setSelectedIds((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id]);
  };

  const toggleVisibleSelected = () => {
    setSelectedIds((current) => {
      if (allVisibleSelected) {
        return current.filter((id) => !records.some((record) => record.id === id));
      }
      const next = new Set(current);
      records.forEach((record) => next.add(record.id));
      return [...next];
    });
  };

  const deleteSelectedRecords = async () => {
    if (!selectedIds.length) {
      return;
    }
    setDeleteBusy(true);
    try {
      const response = await deleteFPVVideoRecords({ ids: selectedIds });
      setSelectedIds([]);
      setDeleteConfirmOpen(false);
      const reloaded = await loadRecords(0, false, false);
      if (reloaded) {
        setBanner(`${t.deletedRecords}: ${response.deleted}`);
      }
    } catch (error) {
      setBanner(error instanceof Error ? error.message : t.deleteFailed);
    } finally {
      setDeleteBusy(false);
    }
  };

  const exportSelectedRecords = async () => {
    if (!selectedExportableIds.length) {
      setBanner(t.exportVideoEmpty);
      return;
    }
    setExporting(true);
    setBanner("");
    try {
      const { blob, fileName } = await exportFPVVideoRecords({ ids: selectedExportableIds });
      downloadBlob(fileName || archiveFileName("fpv-videos"), blob);
    } catch (error) {
      setBanner(error instanceof Error ? error.message : t.exportFailed);
    } finally {
      setExporting(false);
    }
  };

  return (
    <div className={banner ? "screen-management screen-management--fpv-records screen-management--with-banner" : "screen-management screen-management--fpv-records"}>
      <div className="screen-management__header">
        <div className="screen-panel-title">
          <span className="screen-panel-title__icon screen-panel-title__icon--target">
            <FileVideo aria-hidden="true" />
          </span>
          <span className="screen-panel-title__text">
            <em>{t.fpvRecordsView}</em>
            <strong>{t.fpvRecordList}</strong>
          </span>
        </div>
        <div className="screen-management__actions">
          <button type="button" disabled={loading || deleteBusy || exporting} onClick={() => void loadRecords(0, false)}>
            <RefreshCw className={loading ? "app-spinner" : undefined} size={14} aria-hidden="true" />
            <span>{t.refresh}</span>
          </button>
          <button type="button" disabled={!selectedExportableCount || loading || deleteBusy || exporting} onClick={() => void exportSelectedRecords()}>
            {exporting ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <Download size={14} aria-hidden="true" />}
            <span>{exporting ? t.exporting : t.exportVideoFiles}</span>
          </button>
          <button type="button" disabled={!selectedCount || deleteBusy || exporting} onClick={() => setDeleteConfirmOpen(true)}>
            {deleteBusy ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <Trash2 size={14} aria-hidden="true" />}
            <span>{t.deleteSelected}</span>
          </button>
        </div>
      </div>

      <div className="screen-management__summary" aria-label={t.fpvRecordList}>
        <span>{t.recordCount}: {records.length}</span>
        <span>{t.selectedCount}: {selectedCount}</span>
        <span>{t.exportVideoFiles}: {selectedExportableCount}</span>
      </div>

      <div className="screen-management__filters screen-management__filters--fpv-records" aria-label={t.filter}>
        <label>
          <span>{t.signalTypeFilter}</span>
          <input value={signalTypeQuery} onChange={(event) => setSignalTypeQuery(event.target.value)} />
        </label>
        <label>
          <span>{t.dateFrom}</span>
          <input type="date" value={dateFrom} onChange={(event) => {
            const value = event.target.value;
            setDateFrom(value);
            setDateTo((current) => value && current && current < value ? value : current);
          }} />
        </label>
        <label>
          <span>{t.dateTo}</span>
          <input type="date" min={dateFrom || undefined} value={dateTo} onChange={(event) => setDateTo(event.target.value)} />
        </label>
        <button type="button" onClick={() => {
          setSignalTypeQuery("");
          setDateFrom("");
          setDateTo("");
        }}>
          <X size={13} aria-hidden="true" />
          <span>{t.clearFilters}</span>
        </button>
      </div>

      {banner ? <div className="screen-management__banner">{banner}</div> : null}

      <div className="screen-management-table-wrap">
        <table className="screen-management-table screen-management-table--fpv-records">
          <colgroup>
            <col className="screen-management-table__select-col" />
            <col className="screen-management-table__model-col" />
            <col className="screen-management-table__frequency-col" />
            <col className="screen-management-table__signal-col" />
            <col className="screen-management-table__time-col" />
            <col className="screen-management-table__time-col" />
            <col className="screen-management-table__duration-col" />
            <col className="screen-management-table__metric-col" />
            <col className="screen-management-table__metric-col" />
            <col className="screen-management-table__actions-col" />
          </colgroup>
          <thead>
            <tr>
              <th>
                <input type="checkbox" checked={allVisibleSelected} onChange={toggleVisibleSelected} aria-label={t.selectedCount} />
              </th>
              <th>{t.signal}</th>
              <th>{t.frequency}</th>
              <th>{t.rssi}</th>
              <th>{t.firstSeen}</th>
              <th>{t.lastSeen}</th>
              <th>{t.duration}</th>
              <th>{t.fileSize}</th>
              <th>{t.status}</th>
              <th>{t.actions}</th>
            </tr>
          </thead>
          <tbody>
            {records.length ? records.map((record) => {
              const playable = record.status === "ready" && Boolean(record.fileUrl);
              const actionTitle = playable ? t.playVideoFile : record.error || t.recordFailed;
              return (
                <tr key={record.id}>
                  <td>
                    <input type="checkbox" checked={selectedIds.includes(record.id)} onChange={() => toggleRecordSelected(record.id)} aria-label={record.id} />
                  </td>
                  <td>
                    <strong title={record.signalType || "-"}>{record.signalType || "-"}</strong>
                    <small title={record.fileName || record.id}>{record.fileName || record.id}</small>
                  </td>
                  <td>{formatFrequency(record.frequency)}</td>
                  <td>{formatRSSI(record.rssi)}</td>
                  <td>{formatFullTime(record.startedAt, locale)}</td>
                  <td>{formatFullTime(record.endedAt, locale)}</td>
                  <td>{formatDuration(record.durationSeconds)}</td>
                  <td>{formatFileSize(record.fileSizeBytes, locale)}</td>
                  <td>
                    <span className={record.status === "ready" ? "screen-fpv-record-status screen-fpv-record-status--ready" : "screen-fpv-record-status screen-fpv-record-status--failed"} title={record.error || record.status}>
                      {record.status === "ready" ? t.recordReady : t.recordFailed}
                    </span>
                  </td>
                  <td>
                    <div className="screen-table-action-group">
                      <button type="button" className="screen-table-action" onClick={() => setVideoRecord(record)} title={actionTitle}>
                        {playable ? <Play size={13} aria-hidden="true" /> : <Info size={13} aria-hidden="true" />}
                        <span>{playable ? t.play : t.viewRecord}</span>
                      </button>
                    </div>
                  </td>
                </tr>
              );
            }) : (
              <tr>
                <td colSpan={10}>
                  <div className="screen-management-empty">{loading ? t.waiting : t.noFPVVideoRecords}</div>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="screen-management__footer">
        <span>{records.length}</span>
        <button type="button" disabled={!hasMore || loading} onClick={() => void loadRecords(nextOffset, true)}>
          {loading ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <ChevronDown size={14} aria-hidden="true" />}
          <span>{t.loadMore}</span>
        </button>
      </div>

      {deleteConfirmOpen ? (
        <IntrusionDeleteConfirm
          count={selectedCount}
          busy={deleteBusy}
          t={{
            ...t,
            deleteConfirmTitle: t.deleteFPVRecordTitle,
            deleteConfirmMessage: t.deleteFPVRecordMessage,
          }}
          onCancel={() => setDeleteConfirmOpen(false)}
          onConfirm={() => void deleteSelectedRecords()}
        />
      ) : null}

      {videoRecord ? (
        <FPVVideoRecordModal record={videoRecord} locale={locale} t={t} onClose={() => setVideoRecord(null)} />
      ) : null}
    </div>
  );
}

function InterferenceReportsManagement({
  t,
  locale,
  userSettings,
}: {
  t: Record<string, string>;
  locale: Locale;
  userSettings: UserSettings;
}) {
  const pageSize = 50;
  const [reports, setReports] = useState<InterferenceReportSummary[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [nextOffset, setNextOffset] = useState(0);
  const [loading, setLoading] = useState(false);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [statusFilter, setStatusFilter] = useState<"all" | InterferenceReportStatus>("all");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [banner, setBanner] = useState("");
  const [deletingId, setDeletingId] = useState("");
  const [exporting, setExporting] = useState(false);
  const [deleteReport, setDeleteReport] = useState<InterferenceReportSummary | null>(null);
  const loadRequestRef = useRef(0);
  const channelLabels = normalizeScreenStrikeChannelLabels(userSettings.screenStrikeChannelLabels);

  const loadReports = useCallback(async (offset: number, append: boolean, clearBanner = true) => {
    const requestId = loadRequestRef.current + 1;
    loadRequestRef.current = requestId;
    setLoading(true);
    try {
      const response = await getInterferenceReports(pageSize, offset, { status: statusFilter });
      if (requestId !== loadRequestRef.current) {
        return false;
      }
      setReports((current) => append ? appendInterferenceReports(current, response.items) : response.items);
      if (!append) {
        setSelectedIds([]);
      }
      setHasMore(Boolean(response.hasMore));
      setNextOffset(response.nextOffset ?? 0);
      if (clearBanner) {
        setBanner("");
      }
      return true;
    } catch (error) {
      if (requestId === loadRequestRef.current) {
        setBanner(error instanceof Error ? error.message : String(error));
      }
      return false;
    } finally {
      if (requestId === loadRequestRef.current) {
        setLoading(false);
      }
    }
  }, [statusFilter]);

  useEffect(() => {
    void loadReports(0, false);
  }, [loadReports]);

  const visibleReports = useMemo(() => reports.filter((report) => {
    const day = formatDateKey(report.startedAt);
    if (dateFrom && day < dateFrom) {
      return false;
    }
    if (dateTo && day > dateTo) {
      return false;
    }
    return true;
  }), [dateFrom, dateTo, reports]);

  useEffect(() => {
    const visibleIds = new Set(visibleReports.map((report) => report.id));
    setSelectedIds((current) => {
      const next = current.filter((id) => visibleIds.has(id));
      return next.length === current.length ? current : next;
    });
  }, [visibleReports]);

  const runningCount = visibleReports.filter((report) => report.status === "running").length;
  const failedCount = visibleReports.filter((report) => report.status === "failed").length;
  const selectedCount = selectedIds.length;
  const allVisibleSelected = visibleReports.length > 0 && visibleReports.every((report) => selectedIds.includes(report.id));

  const toggleReportSelected = (id: string) => {
    setSelectedIds((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id]);
  };

  const toggleVisibleSelected = () => {
    setSelectedIds((current) => {
      if (allVisibleSelected) {
        return current.filter((id) => !visibleReports.some((report) => report.id === id));
      }
      const next = new Set(current);
      visibleReports.forEach((report) => next.add(report.id));
      return [...next];
    });
  };

  const deleteFailedReport = async () => {
    if (!deleteReport || deleteReport.status !== "failed") {
      return;
    }
    setDeletingId(deleteReport.id);
    try {
      const response = await deleteFailedInterferenceReport(deleteReport.id);
      setReports((items) => items.filter((item) => item.id !== deleteReport.id));
      setSelectedIds((items) => items.filter((id) => id !== deleteReport.id));
      setDeleteReport(null);
      setBanner(`${t.deletedRecords}: ${response.deleted}`);
    } catch (error) {
      setBanner(error instanceof Error ? error.message : t.deleteFailed);
    } finally {
      setDeletingId("");
    }
  };

  const exportReports = async () => {
    setExporting(true);
    setBanner("");
    try {
      const selected = new Set(selectedIds);
      const items = visibleReports.filter((report) => selected.has(report.id));
      if (!items.length) {
        setBanner(t.exportEmpty);
        return;
      }
      downloadCSV(
        reportFileName("interference-reports"),
        interferenceReportsToCSV(items, t, locale, channelLabels),
      );
    } catch (error) {
      setBanner(error instanceof Error ? error.message : t.exportFailed);
    } finally {
      setExporting(false);
    }
  };

  return (
    <div className={banner ? "screen-management screen-management--interference-reports screen-management--with-banner" : "screen-management screen-management--interference-reports"}>
      <div className="screen-management__header">
        <div className="screen-panel-title">
          <span className="screen-panel-title__icon screen-panel-title__icon--strike">
            <Zap aria-hidden="true" />
          </span>
          <span className="screen-panel-title__text">
            <em>{t.interferenceReportsView}</em>
            <strong>{t.interferenceReportList}</strong>
          </span>
        </div>
        <div className="screen-management__actions">
          <button type="button" disabled={loading || Boolean(deletingId) || exporting} onClick={() => void loadReports(0, false)}>
            <RefreshCw className={loading ? "app-spinner" : undefined} size={14} aria-hidden="true" />
            <span>{t.refresh}</span>
          </button>
          <button type="button" disabled={!selectedCount || loading || Boolean(deletingId) || exporting} onClick={() => void exportReports()}>
            {exporting ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <Download size={14} aria-hidden="true" />}
            <span>{exporting ? t.exporting : t.exportReport}</span>
          </button>
        </div>
      </div>

      <div className="screen-management__summary" aria-label={t.interferenceReportList}>
        <span>{t.recordCount}: {visibleReports.length}</span>
        <span>{t.selectedCount}: {selectedCount}</span>
        <span>{t.interferenceReportStatusRunning}: {runningCount}</span>
        <span>{t.interferenceReportStatusFailed}: {failedCount}</span>
      </div>

      <div className="screen-management__filters screen-management__filters--interference-reports" aria-label={t.filter}>
        <label>
          <span>{t.status}</span>
          <select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value as "all" | InterferenceReportStatus)}>
            <option value="all">{t.interferenceReportStatusAll}</option>
            <option value="running">{t.interferenceReportStatusRunning}</option>
            <option value="completed">{t.interferenceReportStatusCompleted}</option>
            <option value="failed">{t.interferenceReportStatusFailed}</option>
            <option value="abnormal">{t.interferenceReportStatusAbnormal}</option>
          </select>
        </label>
        <label>
          <span>{t.dateFrom}</span>
          <input type="date" value={dateFrom} onChange={(event) => {
            const value = event.target.value;
            setDateFrom(value);
            setDateTo((current) => value && current && current < value ? value : current);
          }} />
        </label>
        <label>
          <span>{t.dateTo}</span>
          <input type="date" min={dateFrom || undefined} value={dateTo} onChange={(event) => setDateTo(event.target.value)} />
        </label>
        <button type="button" onClick={() => {
          setStatusFilter("all");
          setDateFrom("");
          setDateTo("");
        }}>
          <X size={13} aria-hidden="true" />
          <span>{t.clearFilters}</span>
        </button>
      </div>

      {banner ? <div className="screen-management__banner">{banner}</div> : null}

      <div className="screen-management-table-wrap">
        <table className="screen-management-table screen-management-table--interference-reports">
          <colgroup>
            <col className="screen-management-table__select-col" />
            <col className="screen-management-table__status-col" />
            <col className="screen-management-table__time-col" />
            <col className="screen-management-table__time-col" />
            <col className="screen-management-table__duration-col" />
            <col className="screen-management-table__identity-col" />
            <col className="screen-management-table__duration-col" />
            <col className="screen-management-table__error-col" />
            <col className="screen-management-table__actions-col" />
          </colgroup>
          <thead>
            <tr>
              <th>
                <input type="checkbox" checked={allVisibleSelected} onChange={toggleVisibleSelected} aria-label={t.selectedCount} />
              </th>
              <th>{t.status}</th>
              <th>{t.firstSeen}</th>
              <th>{t.lastSeen}</th>
              <th>{t.duration}</th>
              <th>{t.interferenceReportChannels}</th>
              <th>{t.interferenceReportRequestedDuration}</th>
              <th>{t.interferenceReportError}</th>
              <th>{t.actions}</th>
            </tr>
          </thead>
          <tbody>
            {visibleReports.length ? visibleReports.map((report) => (
              <tr key={report.id}>
                <td>
                  <input type="checkbox" checked={selectedIds.includes(report.id)} onChange={() => toggleReportSelected(report.id)} aria-label={report.summary || report.id} />
                </td>
                <td>
                  <span className={`screen-interference-report-status screen-interference-report-status--${report.status}`}>
                    {interferenceReportStatusLabel(report.status, t)}
                  </span>
                </td>
                <td>{formatFullTime(report.startedAt, locale)}</td>
                <td>{formatFullTime(report.endedAt, locale)}</td>
                <td>{formatDuration(report.durationSeconds)}</td>
                <td>
                  <strong title={formatInterferenceReportChannels(report, channelLabels)}>
                    {formatInterferenceReportChannels(report, channelLabels)}
                  </strong>
                  <small>{report.channelOutputs?.length ? report.channelOutputs.map((output) => `Y${output}`).join(", ") : report.summary || report.id}</small>
                </td>
                <td>{formatDuration(report.requestedDurationSeconds)}</td>
                <td className={report.lastError || report.abnormalReason ? "screen-table-error-cell" : undefined}>
                  {report.lastError || report.abnormalReason || "-"}
                </td>
                <td>
                  {report.status === "failed" ? (
                    <button
                      type="button"
                      className="screen-table-action screen-table-action--danger"
                      disabled={Boolean(deletingId)}
                      title={t.deleteFailedReport}
                      onClick={() => setDeleteReport(report)}
                    >
                      {deletingId === report.id ? <Loader2 className="app-spinner" size={13} aria-hidden="true" /> : <Trash2 size={13} aria-hidden="true" />}
                      <span>{t.delete}</span>
                    </button>
                  ) : "-"}
                </td>
              </tr>
            )) : (
              <tr>
                <td colSpan={9}>
                  <div className="screen-management-empty">{loading ? t.waiting : t.noInterferenceReports}</div>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="screen-management__footer">
        <span>{visibleReports.length} / {reports.length}</span>
        <button type="button" disabled={!hasMore || loading} onClick={() => void loadReports(nextOffset, true)}>
          {loading ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <ChevronDown size={14} aria-hidden="true" />}
          <span>{t.loadMore}</span>
        </button>
      </div>

      {deleteReport ? (
        <IntrusionDeleteConfirm
          count={1}
          busy={deletingId === deleteReport.id}
          t={{
            ...t,
            deleteConfirmTitle: t.deleteInterferenceReportTitle,
            deleteConfirmMessage: t.deleteInterferenceReportMessage,
          }}
          onCancel={() => setDeleteReport(null)}
          onConfirm={() => void deleteFailedReport()}
        />
      ) : null}
    </div>
  );
}

function IntrusionCoordinateCell({
  record,
  t,
  hasMap,
}: {
  record: IntrusionRecord;
  t: Record<string, string>;
  hasMap: boolean;
}) {
  const parts = intrusionCoordinateParts(record, t);

  if (!parts.length && !hasMap) {
    return <span className="screen-intrusion-coordinate-empty">-</span>;
  }
  const trackPointCount = intrusionTrackPointCount(record);
  const visibleParts = parts.length ? parts : [{
    key: "trajectory",
    label: t.trajectoryCount,
    value: String(trackPointCount),
  }];

  return (
    <div className="screen-intrusion-coordinate">
      <div className="screen-intrusion-coordinate__chips" title={visibleParts.map((part) => `${part.label}: ${part.value}`).join(" / ")}>
        {visibleParts.map((part) => (
          <span key={part.key} title={`${part.label}: ${part.value}`}>
            <em>{part.label}</em>
            <strong>{part.value}</strong>
          </span>
        ))}
      </div>
    </div>
  );
}

function IntrusionReplayCell({
  record,
  t,
  hasMap,
  onOpenMap,
}: {
  record: IntrusionRecord;
  t: Record<string, string>;
  hasMap: boolean;
  onOpenMap: (record: IntrusionRecord) => void;
}) {
  if (!hasMap) {
    return <span className="screen-intrusion-coordinate-empty">-</span>;
  }
  return (
    <button
      type="button"
      className="screen-intrusion-coordinate__map"
      onClick={() => onOpenMap(record)}
      title={t.trajectoryReplay}
      aria-label={t.trajectoryReplay}
    >
      <MapPinned size={13} aria-hidden="true" />
      <span>{t.trajectoryReplay}</span>
    </button>
  );
}

function IntrusionDeleteConfirm({
  count,
  busy,
  t,
  onCancel,
  onConfirm,
}: {
  count: number;
  busy: boolean;
  t: Record<string, string>;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div className="app-modal-backdrop" role="presentation" onClick={onCancel}>
      <section className="app-modal-card screen-intrusion-delete-modal" role="dialog" aria-modal="true" aria-labelledby="screen-intrusion-delete-title" onClick={(event) => event.stopPropagation()}>
        <button type="button" className="screen-navigation-modal__close" onClick={onCancel} aria-label={t.close}>
          <X size={15} aria-hidden="true" />
        </button>
        <header>
          <span>{t.deleteSelected}</span>
          <h2 id="screen-intrusion-delete-title">{t.deleteConfirmTitle}</h2>
        </header>
        <p>{t.deleteConfirmMessage.replace("{count}", String(count))}</p>
        <div className="screen-intrusion-delete-modal__actions">
          <button type="button" onClick={onCancel} disabled={busy}>{t.cancel}</button>
          <button type="button" className="screen-table-action--danger" onClick={onConfirm} disabled={busy || count <= 0}>
            {busy ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <Trash2 size={14} aria-hidden="true" />}
            <span>{t.delete}</span>
          </button>
        </div>
      </section>
    </div>
  );
}

function FPVVideoRecordModal({
  record,
  locale,
  t,
  onClose,
}: {
  record: FPVVideoRecord;
  locale: Locale;
  t: Record<string, string>;
  onClose: () => void;
}) {
  const title = `${record.signalType || t.unknown} / ${formatFrequency(record.frequency)}`;
  const playable = record.status === "ready" && Boolean(record.fileUrl);
  const modal = (
    <div className="app-modal-backdrop" role="presentation" onClick={onClose}>
      <section className="app-modal-card screen-fpv-record-modal" role="dialog" aria-modal="true" aria-labelledby="screen-fpv-record-title" onClick={(event) => event.stopPropagation()}>
        <button type="button" className="screen-navigation-modal__close" onClick={onClose} aria-label={t.close}>
          <X size={15} aria-hidden="true" />
        </button>
        <header className="screen-fpv-record-modal__header">
          <span>{playable ? t.playVideoFile : t.recordFailed}</span>
          <h2 id="screen-fpv-record-title">{title}</h2>
          <p>{formatFullTime(record.startedAt, locale)} / {formatDuration(record.durationSeconds)}</p>
        </header>
        {playable ? (
          <video className="screen-fpv-record-modal__video" src={record.fileUrl} controls autoPlay playsInline />
        ) : (
          <div className="screen-fpv-record-modal__error">
            <Info size={28} aria-hidden="true" />
            <strong>{t.failureReason}</strong>
            <p>{record.error || record.status || t.recordFailed}</p>
          </div>
        )}
      </section>
    </div>
  );
  return createPortal(modal, document.body);
}

function IntrusionMapModal({
  record,
  locale,
  t,
  userSettings,
  onClose,
}: {
  record: IntrusionRecord;
  locale: Locale;
  t: Record<string, string>;
  userSettings: UserSettings;
  onClose: () => void;
}) {
  const target = intrusionToPositionTarget(record);
  const deviceLocation = record.deviceLocation ?? null;
  const title = resolveDisplayModel(record) || record.serial || t.intrusionMapTitle;

  const modal = (
    <div className="app-modal-backdrop" role="presentation" onClick={onClose}>
      <section className="app-modal-card screen-intrusion-map-modal" role="dialog" aria-modal="true" aria-labelledby="screen-intrusion-map-title" onClick={(event) => event.stopPropagation()}>
        <button type="button" className="screen-navigation-modal__close" onClick={onClose} aria-label={t.close}>
          <X size={15} aria-hidden="true" />
        </button>
        <header className="screen-intrusion-map-modal__header">
          <span>{t.intrusionMapTitle}</span>
          <h2 id="screen-intrusion-map-title">{title}</h2>
          <p>{record.serial || record.targetId || "-"}</p>
        </header>
        <div className="screen-intrusion-map-modal__map">
          <ScreenMapLegend t={t} />
          <ScreenMap
            positions={[target]}
            selectedPosition={target}
            deviceLocation={deviceLocation}
            whitelist={userSettings.whitelist}
            onSelectPosition={() => undefined}
            t={t}
            locale={locale}
            showLayerControl={false}
          />
        </div>
      </section>
    </div>
  );

  return createPortal(modal, document.body);
}

function WhitelistManagement({
  t,
  locale,
  userSettings,
  onSaveUserSettings,
}: {
  t: Record<string, string>;
  locale: Locale;
  userSettings: UserSettings;
  onSaveUserSettings: (settings: UserSettings) => Promise<UserSettings>;
}) {
  const whitelist = userSettings.whitelist ?? [];
  const sortedWhitelist = useMemo(() => [...whitelist].sort((left, right) => Date.parse(right.createdAt ?? "") - Date.parse(left.createdAt ?? "")), [whitelist]);
  const [serialDraft, setSerialDraft] = useState("");
  const [modelDraft, setModelDraft] = useState("");
  const [editingSerial, setEditingSerial] = useState("");
  const [editSerialDraft, setEditSerialDraft] = useState("");
  const [editModelDraft, setEditModelDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [banner, setBanner] = useState("");

  const saveWhitelist = async (nextWhitelist: WhitelistItem[], success: string) => {
    setSaving(true);
    try {
      await onSaveUserSettings({ whitelist: nextWhitelist });
      setBanner(success);
      return true;
    } catch (error) {
      setBanner(error instanceof Error ? error.message : t.saveFailed);
      return false;
    } finally {
      setSaving(false);
    }
  };

  const addWhitelist = async () => {
    if (!serialDraft.trim()) {
      setBanner(t.whitelistSerialRequired);
      return;
    }
    const saved = await saveWhitelist(upsertWhitelistItem(whitelist, {
      serial: serialDraft,
      model: modelDraft,
      source: "manual",
    }), t.whitelistSaved);
    if (!saved) {
      return;
    }
    setSerialDraft("");
    setModelDraft("");
  };

  const startEdit = (item: WhitelistItem) => {
    setEditingSerial(item.serial);
    setEditSerialDraft(item.serial);
    setEditModelDraft(item.model ?? "");
    setBanner("");
  };

  const cancelEdit = () => {
    setEditingSerial("");
    setEditSerialDraft("");
    setEditModelDraft("");
  };

  const saveEdit = async () => {
    if (!editSerialDraft.trim()) {
      setBanner(t.whitelistSerialRequired);
      return;
    }
    const saved = await saveWhitelist(updateWhitelistItem(whitelist, editingSerial, {
      serial: editSerialDraft,
      model: editModelDraft,
      source: "manual",
    }), t.whitelistSaved);
    if (!saved) {
      return;
    }
    cancelEdit();
  };

  const deleteWhitelist = async (serial: string) => {
    const saved = await saveWhitelist(removeWhitelistSerial(whitelist, serial), t.whitelistDeleted);
    if (saved && normalizeWhitelistSerial(serial) === normalizeWhitelistSerial(editingSerial)) {
      cancelEdit();
    }
  };

  return (
    <div className={banner ? "screen-management screen-management--whitelist screen-management--with-banner" : "screen-management screen-management--whitelist"}>
      <div className="screen-management__header">
        <div className="screen-panel-title">
          <span className="screen-panel-title__icon screen-panel-title__icon--target">
            <ShieldCheck aria-hidden="true" />
          </span>
          <span className="screen-panel-title__text">
            <em>{t.whitelistView}</em>
            <strong>{t.whitelistManagement}</strong>
          </span>
        </div>
        <strong className="screen-management__count">{whitelist.length}</strong>
      </div>

      <div className="screen-management__filters screen-management__filters--entry">
        <label>
          <span>{t.serial}</span>
          <input value={serialDraft} onChange={(event) => setSerialDraft(event.target.value)} />
        </label>
        <label>
          <span>{t.model}</span>
          <input value={modelDraft} onChange={(event) => setModelDraft(event.target.value)} />
        </label>
        <button type="button" disabled={saving} onClick={() => void addWhitelist()}>
          {saving ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <Plus size={14} aria-hidden="true" />}
          <span>{t.add}</span>
        </button>
      </div>

      {banner ? <div className="screen-management__banner">{banner}</div> : null}

      <div className="screen-management-table-wrap">
        <table className="screen-management-table screen-management-table--whitelist">
          <thead>
            <tr>
              <th>{t.serial}</th>
              <th>{t.model}</th>
              <th>{t.createdAt}</th>
              <th>{t.actions}</th>
            </tr>
          </thead>
          <tbody>
            {sortedWhitelist.length ? sortedWhitelist.map((item) => {
              const editing = normalizeWhitelistSerial(item.serial) === normalizeWhitelistSerial(editingSerial);
              return (
                <tr key={item.serial}>
                  <td>
                    {editing ? (
                      <input className="screen-management-inline-input" value={editSerialDraft} onChange={(event) => setEditSerialDraft(event.target.value)} />
                    ) : item.serial}
                  </td>
                  <td>
                    {editing ? (
                      <input className="screen-management-inline-input" value={editModelDraft} onChange={(event) => setEditModelDraft(event.target.value)} />
                    ) : item.model || "-"}
                  </td>
                  <td>{formatFullTime(item.createdAt, locale)}</td>
                  <td>
                    <div className="screen-table-action-group">
                      {editing ? (
                        <>
                          <button type="button" className="screen-table-action screen-table-action--active" disabled={saving} onClick={() => void saveEdit()}>
                            <Check size={13} aria-hidden="true" />
                            <span>{t.save}</span>
                          </button>
                          <button type="button" className="screen-table-action" disabled={saving} onClick={cancelEdit}>
                            <X size={13} aria-hidden="true" />
                            <span>{t.cancel}</span>
                          </button>
                        </>
                      ) : (
                        <>
                          <button type="button" className="screen-table-action" disabled={saving} onClick={() => startEdit(item)}>
                            <Edit3 size={13} aria-hidden="true" />
                            <span>{t.edit}</span>
                          </button>
                          <button type="button" className="screen-table-action screen-table-action--danger" disabled={saving} onClick={() => void deleteWhitelist(item.serial)}>
                            <Trash2 size={13} aria-hidden="true" />
                            <span>{t.delete}</span>
                          </button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              );
            }) : (
              <tr>
                <td colSpan={4}>
                  <div className="screen-management-empty">{t.noWhitelist}</div>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function DeviceSummary({
  location,
  t,
  locale,
  onOpenManualLocation,
  manualLocationPickMode,
  onManualLocationPickToggle,
}: {
  location: ScreenDeviceLocationResponse | null;
  t: Record<string, string>;
  locale: Locale;
  onOpenManualLocation: () => void;
  manualLocationPickMode: boolean;
  onManualLocationPickToggle: () => void;
}) {
  const valid = Boolean(location?.valid && location.point);
  const pointText = valid && location?.point ? formatPoint(location.point) : t.noLocation;
  const locationState = valid
    ? location?.source === "manual"
      ? t.manualLocation
      : location?.locked
        ? t.locked
        : t.unlocked
    : t.noLocation;
  const showManualAction = !valid || location?.source === "manual";
  const manualActionLabel = location?.source === "manual" ? t.editManualLocation : t.setManualLocation;
  const temperatureText = [
    formatTemperature(location?.rfTempC, t.rfTemp, locale),
    location?.mainTempC === undefined ? "" : formatTemperature(location.mainTempC, t.mainTemp, locale),
  ].filter(Boolean).join(" / ");

  return (
    <article className={`screen-device-summary screen-device-summary--${valid ? "located" : "empty"}`}>
      <span className="screen-device-summary__icon">
        <Crosshair aria-hidden="true" />
      </span>
      <span className="screen-device-summary__body">
        <span className="screen-device-summary__title">
          <strong>{t.deviceInfo}</strong>
          <em>{locationState}</em>
        </span>
        <span className="screen-device-summary__point" title={pointText}>{pointText}</span>
        {temperatureText ? <small title={temperatureText}>{temperatureText}</small> : null}
      </span>
      <span className="screen-device-summary__actions">
        {showManualAction ? (
          <button className="screen-device-summary__manual" type="button" onClick={onOpenManualLocation}>
            <MapPin aria-hidden="true" />
            <span>{manualActionLabel}</span>
          </button>
        ) : null}
        <button
          className={manualLocationPickMode ? "screen-device-summary__manual screen-device-summary__manual--active" : "screen-device-summary__manual"}
          type="button"
          aria-pressed={manualLocationPickMode}
          onClick={onManualLocationPickToggle}
        >
          <LocateFixed aria-hidden="true" />
          <span>{manualLocationPickMode ? t.cancelPickManualLocation : t.pickManualLocation}</span>
        </button>
      </span>
    </article>
  );
}

function TabStatusDot({ status }: { status?: TCPListenerStatus }) {
  return <span className={`screen-tab__status screen-tab__status--${getTCPListenerStatusTone(status)}`} aria-hidden="true" />;
}

function TCPClientStatusDot({ status }: { status?: TCPClientStatus }) {
  return <span className={`screen-tab__status screen-tab__status--${getTCPClientStatusTone(status)}`} aria-hidden="true" />;
}

function getTCPListenerStatusTone(status?: TCPListenerStatus) {
  if (status?.sourceConnected) {
    return "success";
  }
  if (status?.listening) {
    return "warning";
  }
  return "danger";
}

function getTCPClientStatusTone(status?: TCPClientStatus) {
  if (status?.connected) {
    return "success";
  }
  if (!status?.updatedAt && !status?.connectError) {
    return "warning";
  }
  return "danger";
}

function PositionCard({
  target,
  whitelisted,
  alert,
  whitelistBusy,
  selected,
  t,
  locale,
  now,
  expireSeconds,
  onSelect,
  onOpenNavigationQRCode,
  onToggleWhitelist,
}: {
  target: ScreenPositionTarget;
  whitelisted: boolean;
  alert: boolean;
  whitelistBusy: boolean;
  selected: boolean;
  t: Record<string, string>;
  locale: Locale;
  now: Date;
  expireSeconds: number;
  onSelect: () => void;
  onOpenNavigationQRCode?: (label: string, point: ScreenPositionPoint) => void;
  onToggleWhitelist: (target: ScreenPositionTarget) => void;
}) {
  const timeTone = getTargetTimeTone(target.lastSeen, now, expireSeconds);
  const pendingEncrypted = isPendingEncryptedDJIDrone(target);
  const remainingSeconds = targetDisappearRemainingSeconds(target.lastSeen, now, expireSeconds);
  const showCountdown = shouldShowDisappearCountdown(timeTone);
  const whitelistDisabled = whitelistBusy || !target.serial.trim() || (pendingEncrypted && !whitelisted);
  const statusIconClassName = [
    "screen-position-card__status-icon",
    whitelisted ? "screen-position-card__status-icon--whitelist" : alert ? "screen-position-card__status-icon--alert" : "",
  ].filter(Boolean).join(" ");
  const cardClassName = [
    "screen-position-card",
    whitelisted ? "screen-position-card--whitelist" : alert ? "screen-position-card--alert" : "",
    selected ? "screen-position-card--selected" : "",
  ].filter(Boolean).join(" ");

  return (
    <article
      className={cardClassName}
      role="button"
      tabIndex={0}
      onClick={onSelect}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onSelect();
        }
      }}
    >
      <div className="screen-position-card__head">
        <span className="screen-position-card__identity">
          <span className="screen-position-card__title-row">
            <strong>{target.model || t.unknown}</strong>
            {whitelisted ? (
              <span className="screen-position-card__whitelist-badge">
                <ShieldCheck size={11} aria-hidden="true" />
                {t.whitelisted}
              </span>
            ) : null}
            {pendingEncrypted ? (
              <span className="screen-position-card__parsing">
                <span aria-hidden="true" />
                {t.parsingTarget}
              </span>
            ) : null}
          </span>
          <span className="screen-position-card__fingerprint" title={target.serial || target.id || t.unknown}>
            <strong>{target.serial || target.id || t.unknown}</strong>
          </span>
        </span>
        <span className="screen-position-card__actions">
          <button
            className={whitelisted ? "screen-whitelist-button screen-whitelist-button--active" : "screen-whitelist-button"}
            type="button"
            disabled={whitelistDisabled}
            title={pendingEncrypted && !whitelisted ? t.parsingTarget : whitelisted ? t.removeFromWhitelist : t.addToWhitelist}
            onClick={(event) => {
              event.stopPropagation();
              onToggleWhitelist(target);
            }}
            onKeyDown={(event) => event.stopPropagation()}
          >
            {whitelistBusy ? (
              <Loader2 className="app-spinner" size={12} aria-hidden="true" />
            ) : whitelisted ? (
              <ShieldMinus size={12} aria-hidden="true" />
            ) : (
              <ShieldPlus size={12} aria-hidden="true" />
            )}
            <span>{whitelisted ? t.removeFromWhitelist : t.addToWhitelist}</span>
          </button>
          <span className={`screen-position-card__time screen-position-card__time--${timeTone}`}>
            {formatTargetTime(target.lastSeen, locale)}
          </span>
        </span>
      </div>

      {showCountdown ? (
        <span className={`screen-target-countdown screen-target-countdown--${timeTone}`}>
          <TimerReset size={12} aria-hidden="true" />
          <em>{t.targetDisappearCountdown}</em>
          <strong>{remainingSeconds === null ? "--:--" : formatCountdown(remainingSeconds)}</strong>
        </span>
      ) : null}

      {pendingEncrypted ? (
        <div className="screen-target-readouts screen-position-card__metrics screen-position-card__metrics--pending">
          <Readout label={t.frequency} value={formatFrequency(target.frequency)} />
          <Readout label={t.rssi} value={formatRSSI(target.rssi)} />
          <Readout label={t.firstSeen} value={formatTargetTime(target.firstSeen, locale)} />
        </div>
      ) : (
        <>
          <div className="screen-position-card__location">
            <span className="screen-position-card__image">
              <span className="screen-position-card__image-glow" />
              <img src={getPositionDroneImageUrl(target.model)} alt="" aria-hidden="true" />
              <span
                className={statusIconClassName}
                title={whitelisted ? t.whitelisted : alert ? t.unwhitelistedDrone : t.drone}
              >
                <img src={alert ? uavBlackFlyIconUrl : uavIconUrl} alt="" aria-hidden="true" />
              </span>
            </span>
            <span className="screen-position-card__grid">
              <CoordinateLine label={t.drone} point={target.drone} t={t} onOpenNavigationQRCode={onOpenNavigationQRCode} />
              <CoordinateLine label={t.pilot} point={target.pilot} t={t} onOpenNavigationQRCode={onOpenNavigationQRCode} />
              <CoordinateLine label={t.home} point={target.home} t={t} onOpenNavigationQRCode={onOpenNavigationQRCode} />
            </span>
          </div>

          <div className="screen-target-readouts screen-position-card__relations">
            <Readout label={t.pilotDistance} value={formatMeters(target.pilotDistanceM, locale, t)} />
            <Readout label={t.droneDistance} value={formatMeters(target.droneDistanceM, locale, t)} />
          </div>

          <div className="screen-target-readouts screen-position-card__metrics">
            <Readout label={t.frequency} value={formatFrequency(target.frequency)} />
            <Readout label={t.rssi} value={formatRSSI(target.rssi)} />
            <Readout label={t.height} value={formatMeters(target.height, locale, t)} />
            <Readout label={t.altitude} value={formatMeters(target.altitude, locale, t)} />
            <Readout label={t.speed} value={formatSpeed(target.speed, locale, t)} />
            <Readout label={t.firstSeen} value={formatTargetTime(target.firstSeen, locale)} />
          </div>
        </>
      )}
    </article>
  );
}

function FPVTable({
  targets,
  t,
  now,
  videoAvailable,
  videoBusy,
  videoOpeningId,
  onViewVideo,
}: {
  targets: ScreenFPVTarget[];
  t: Record<string, string>;
  now: Date;
  videoAvailable: boolean;
  videoBusy: boolean;
  videoOpeningId: string;
  onViewVideo: (target: ScreenFPVTarget) => void;
}) {
  return (
    <div className="screen-fpv-table" role="table">
      <div className="screen-fpv-table__head" role="row">
        <span role="columnheader">{t.signal}</span>
        <span role="columnheader">{t.frequency}</span>
        <span role="columnheader">{t.rssi}</span>
        <span role="columnheader">{t.lastSeen}</span>
        <span role="columnheader">{t.viewVideo}</span>
      </div>
      <div className="screen-fpv-table__body" role="rowgroup">
        {targets.map((target) => {
          const opening = videoOpeningId === target.id;
          const videoDisabled = !videoAvailable || videoBusy || videoOpeningId !== "";
          const videoTitle = !videoAvailable ? t.videoUnavailable : videoBusy ? t.videoBusy : t.viewVideo;
          return (
            <div
              key={target.id}
              className={target.valid ? "screen-fpv-row" : "screen-fpv-row screen-fpv-row--invalid"}
              role="row"
            >
              <span className="screen-fpv-row__signal" data-label={t.signal} role="cell">
                <strong>{target.signalType || t.unknown}</strong>
                {target.deviceSn ? <em>{target.deviceSn}</em> : null}
              </span>
              <span className="screen-fpv-row__value" data-label={t.frequency} role="cell">{formatFrequency(target.frequency)}</span>
              <span className="screen-fpv-row__strength" data-label={t.rssi} role="cell">
                <strong>{formatRSSI(target.rssi)}</strong>
                <span className="screen-fpv-row__meter" aria-hidden="true">
                  <span style={{ width: `${rssiPercent(target.rssi)}%` }} />
                </span>
              </span>
              <span className="screen-fpv-row__value" data-label={t.lastSeen} role="cell">
                {formatAge(target.lastSeen, now, t)}
              </span>
              <span className="screen-fpv-row__action" data-label={t.viewVideo} role="cell">
                <button
                  className={opening ? "screen-fpv-row__video screen-fpv-row__video--opening" : "screen-fpv-row__video"}
                  type="button"
                  disabled={videoDisabled}
                  title={videoTitle}
                  onClick={() => onViewVideo(target)}
                >
                  {opening ? <Loader2 className="app-spinner" size={13} aria-hidden="true" /> : <Play size={13} aria-hidden="true" />}
                  <span>{t.viewVideo}</span>
                </button>
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function CoordinateLine({
  label,
  point,
  t,
  onOpenNavigationQRCode,
}: {
  label: string;
  point?: ScreenPositionPoint;
  t: Record<string, string>;
  onOpenNavigationQRCode?: (label: string, point: ScreenPositionPoint) => void;
}) {
  if (validMapPoint(point) && onOpenNavigationQRCode) {
    const coordinateText = formatNavigationCoordinates(point);
    return (
      <button
        className="screen-position-card__point screen-position-card__point--clickable"
        type="button"
        title={t.navigationQRCode}
        aria-label={`${label} ${coordinateText} ${t.navigationQRCode}`}
        onClick={(event) => {
          event.stopPropagation();
          onOpenNavigationQRCode(label, point);
        }}
        onKeyDown={(event) => event.stopPropagation()}
      >
        <em>{label}</em>
        <strong>
          <small>{t.latitudeShort}</small>
          {formatCoordinateValue(point.latitude)}
        </strong>
        <strong>
          <small>{t.longitudeShort}</small>
          {formatCoordinateValue(point.longitude)}
        </strong>
        <span className="screen-position-card__point-action" aria-hidden="true">
          <QrCode size={11} />
        </span>
      </button>
    );
  }

  return (
    <span className="screen-position-card__point">
      <em>{label}</em>
      <strong>
        <small>{t.latitudeShort}</small>
        {formatCoordinateValue(point?.latitude)}
      </strong>
      <strong>
        <small>{t.longitudeShort}</small>
        {formatCoordinateValue(point?.longitude)}
      </strong>
    </span>
  );
}

function Readout({ label, value }: { label: string; value: string }) {
  return (
    <span className="screen-target-readout">
      <em>{label}</em>
      <strong>{value}</strong>
    </span>
  );
}

function EmptyState({ icon, text }: { icon: ReactNode; text: string }) {
  return (
    <div className="screen-empty">
      <span className="screen-empty__icon">{icon}</span>
      <span>{text}</span>
    </div>
  );
}

function NavigationQRCodeModal({
  state,
  loading,
  error,
  t,
  onClose,
}: {
  state: NavigationQRCodeState | null;
  loading: boolean;
  error: string;
  t: Record<string, string>;
  onClose: () => void;
}) {
  if (!state) {
    return null;
  }

  return (
    <div className="screen-navigation-modal app-modal-backdrop" role="presentation" onClick={onClose}>
      <section
        className="screen-navigation-modal__card app-modal-card"
        role="dialog"
        aria-modal="true"
        aria-labelledby="screen-navigation-modal-title"
        onClick={(event) => event.stopPropagation()}
      >
        <button className="screen-navigation-modal__close" type="button" aria-label={t.close} onClick={onClose}>
          <X size={16} aria-hidden="true" />
        </button>

        <div className="screen-navigation-modal__header">
          <span className="screen-navigation-modal__eyebrow">{t.navigationQRCode}</span>
          <h2 id="screen-navigation-modal-title">{state.label}</h2>
        </div>

        <div className="screen-navigation-modal__body">
          <div className="screen-navigation-modal__coordinate-grid">
            <div className="screen-navigation-modal__coordinate-item">
              <span>{t.navigationCoordinateOriginal}</span>
              <strong>{t.navigationCoordinateSystemWGS84}</strong>
              <code>{formatNavigationCoordinates(state.point)}</code>
            </div>
            <div className="screen-navigation-modal__coordinate-item">
              <span>{t.navigationCoordinateConverted}</span>
              <strong>{t.navigationCoordinateSystemGCJ02}</strong>
              <code>{formatNavigationCoordinates(state.convertedPoint)}</code>
            </div>
          </div>

          <div className="screen-navigation-modal__qr-grid" aria-busy={loading}>
            {navigationMapProviders.map((provider) => {
              const item = state.items.find((current) => current.provider === provider.id);
              const providerLabel = t[provider.labelKey] ?? provider.labelKey;

              return (
                <div key={provider.id} className="screen-navigation-modal__qr-item">
                  <strong>{providerLabel}</strong>
                  {item ? (
                    <span className="screen-navigation-modal__qr-coordinate">
                      {t[item.coordinateLabelKey] ?? item.coordinateLabelKey} / {item.coordinateSystem}: {formatNavigationCoordinates(item.coordinate)}
                    </span>
                  ) : null}
                  <div className="screen-navigation-modal__qr">
                    {loading ? (
                      <div className="screen-navigation-modal__loading">
                        <Loader2 className="app-spinner" size={22} aria-hidden="true" />
                        <span>{t.generatingQRCode}</span>
                      </div>
                    ) : item?.dataUrl ? (
                      <img src={item.dataUrl} alt={providerLabel} loading="lazy" decoding="async" />
                    ) : (
                      <QrCode className="screen-navigation-modal__fallback-icon" size={46} aria-hidden="true" />
                    )}
                  </div>
                </div>
              );
            })}
          </div>

          <p className={error ? "screen-navigation-modal__tip screen-navigation-modal__tip--error" : "screen-navigation-modal__tip"}>
            {error || t.scanToNavigate}
          </p>
        </div>
      </section>
    </div>
  );
}

function ManualDeviceLocationModal({
  open,
  draft,
  saving,
  error,
  canClear,
  t,
  onDraftChange,
  onSave,
  onClear,
  onClose,
}: {
  open: boolean;
  draft: ManualLocationDraft;
  saving: boolean;
  error: string;
  canClear: boolean;
  t: Record<string, string>;
  onDraftChange: (field: keyof ManualLocationDraft, value: string) => void;
  onSave: () => void;
  onClear: () => void;
  onClose: () => void;
}) {
  if (!open) {
    return null;
  }

  return (
    <div className="screen-manual-location-modal app-modal-backdrop" role="presentation" onClick={onClose}>
      <form
        className="screen-manual-location-modal__card app-modal-card"
        role="dialog"
        aria-modal="true"
        aria-labelledby="screen-manual-location-modal-title"
        onClick={(event) => event.stopPropagation()}
        onSubmit={(event) => {
          event.preventDefault();
          onSave();
        }}
      >
        <button className="screen-navigation-modal__close" type="button" aria-label={t.close} disabled={saving} onClick={onClose}>
          <X size={16} aria-hidden="true" />
        </button>

        <div className="screen-navigation-modal__header">
          <span className="screen-navigation-modal__eyebrow">{t.manualLocation}</span>
          <h2 id="screen-manual-location-modal-title">{t.manualLocationTitle}</h2>
        </div>

        <div className="screen-manual-location-modal__fields">
          <label>
            <span>{t.latitude}</span>
            <input
              autoFocus
              type="text"
              inputMode="decimal"
              data-keyboard="numeric"
              pattern="-?[0-9]*[.,]?[0-9]*"
              value={draft.latitude}
              placeholder="39.909181"
              onChange={(event) => onDraftChange("latitude", event.target.value)}
            />
          </label>
          <label>
            <span>{t.longitude}</span>
            <input
              type="text"
              inputMode="decimal"
              data-keyboard="numeric"
              pattern="-?[0-9]*[.,]?[0-9]*"
              value={draft.longitude}
              placeholder="116.397472"
              onChange={(event) => onDraftChange("longitude", event.target.value)}
            />
          </label>
        </div>

        {error ? <p className="screen-navigation-modal__tip screen-navigation-modal__tip--error">{error}</p> : null}

        <div className="screen-manual-location-modal__actions">
          <button className="screen-manual-location-modal__button screen-manual-location-modal__button--ghost" type="button" disabled={saving} onClick={onClose}>
            {t.cancel}
          </button>
          <button className="screen-manual-location-modal__button screen-manual-location-modal__button--danger" type="button" disabled={saving || !canClear} onClick={onClear}>
            {t.clear}
          </button>
          <button className="screen-manual-location-modal__button screen-manual-location-modal__button--primary" type="submit" disabled={saving}>
            {saving ? <Loader2 className="app-spinner" size={14} aria-hidden="true" /> : <MapPin size={14} aria-hidden="true" />}
            <span>{t.save}</span>
          </button>
        </div>
      </form>
    </div>
  );
}

function FPVVideoModal({
  target,
  playbackURL,
  sessionToken,
  closing,
  t,
  locale,
  onClose,
}: {
  target: ScreenFPVTarget | null;
  playbackURL: string;
  sessionToken: string;
  closing: boolean;
  t: Record<string, string>;
  locale: Locale;
  onClose: () => void;
}) {
  const stageRef = useRef<HTMLDivElement | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const whepResourceRef = useRef("");
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const source = target && playbackURL && sessionToken ? appendVideoSessionParam(playbackURL, sessionToken) : "";

  useEffect(() => {
    if (!target) {
      return;
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose, target]);

  useEffect(() => {
    if (!target) {
      return;
    }
    setStatus("loading");
    setError("");
    if (!source) {
      setStatus("error");
      setError(t.videoUnavailable);
    }
  }, [source, t.videoUnavailable, target]);

  useEffect(() => {
    if (!target || !source) {
      return;
    }

    let cancelled = false;
    const peer = new RTCPeerConnection();
    const abortController = new AbortController();
    const remoteStream = new MediaStream();
    whepResourceRef.current = "";

    const video = videoRef.current;
    if (video) {
      video.srcObject = remoteStream;
    }

    const fail = (message: string) => {
      if (cancelled) {
        return;
      }
      setStatus("error");
      setError(message);
    };

    peer.addTransceiver("video", { direction: "recvonly" });
    peer.addTransceiver("audio", { direction: "recvonly" });
    peer.ontrack = (event) => {
      if (cancelled) {
        return;
      }
      const [stream] = event.streams;
      const currentVideo = videoRef.current;
      if (stream && currentVideo && currentVideo.srcObject !== stream) {
        currentVideo.srcObject = stream;
      } else if (!stream) {
        remoteStream.addTrack(event.track);
      }
      void currentVideo?.play().catch(() => undefined);
      setStatus("ready");
    };
    peer.onconnectionstatechange = () => {
      if (peer.connectionState === "failed") {
        fail(t.videoError);
      }
    };

    const start = async () => {
      try {
        const offer = await peer.createOffer();
        await peer.setLocalDescription(offer);
        await waitForICEGatheringComplete(peer, 1500);
        if (cancelled) {
          return;
        }
        const localDescription = peer.localDescription;
        if (!localDescription?.sdp) {
          throw new Error(t.videoError);
        }
        const response = await fetch(source, {
          method: "POST",
          headers: {
            Accept: "application/sdp",
            "Content-Type": "application/sdp",
          },
          signal: abortController.signal,
          body: localDescription.sdp,
        });
        if (!response.ok) {
          throw new Error(await readTextOrStatus(response));
        }
        const resourceURL = response.headers.get("Location") ?? "";
        if (cancelled) {
          deleteWHEPResource(resourceURL);
          return;
        }
        whepResourceRef.current = resourceURL;
        const answer = await response.text();
        if (!answer.trim()) {
          throw new Error(t.videoError);
        }
        await peer.setRemoteDescription({ type: "answer", sdp: answer });
      } catch (error) {
        fail(error instanceof Error ? error.message : String(error));
      }
    };

    void start();

    return () => {
      cancelled = true;
      abortController.abort();
      const resourceURL = whepResourceRef.current;
      whepResourceRef.current = "";
      peer.ontrack = null;
      peer.onconnectionstatechange = null;
      peer.close();
      deleteWHEPResource(resourceURL);
      const currentVideo = videoRef.current;
      const stream = currentVideo?.srcObject;
      if (stream instanceof MediaStream) {
        stream.getTracks().forEach((track) => track.stop());
      }
      if (currentVideo) {
        currentVideo.srcObject = null;
      }
    };
  }, [source, t.videoError, target]);

  if (!target) {
    return null;
  }

  const toggleFullscreen = () => {
    if (document.fullscreenElement) {
      void document.exitFullscreen().catch(() => undefined);
      return;
    }
    void stageRef.current?.requestFullscreen?.().catch(() => undefined);
  };

  return (
    <div className="screen-fpv-video-modal" role="dialog" aria-modal="true" aria-labelledby="screen-fpv-video-title">
      <div className="screen-fpv-video-modal__stage" ref={stageRef}>
        {source ? (
          <video
            ref={videoRef}
            title={`${t.fpvVideo} ${target.signalType || t.unknown}`}
            autoPlay
            muted
            playsInline
            onLoadedData={() => setStatus("ready")}
            onError={() => {
              setStatus("error");
              setError(t.videoError);
            }}
          />
        ) : null}
        <div className="screen-fpv-video-modal__hud">
          <div className="screen-fpv-video-modal__title">
            <span>
              <Maximize2 size={13} aria-hidden="true" />
              {t.fpvVideo}
            </span>
            <h2 id="screen-fpv-video-title">
              {target.signalType || t.unknown} / {formatFrequency(target.frequency)}
            </h2>
            <em>{formatTargetTime(target.lastSeen, locale)}</em>
          </div>
          <div className="screen-fpv-video-modal__stats" aria-hidden="true">
            <span>{t.frequency}: {formatFrequency(target.frequency)}</span>
            <span>{t.rssi}: {formatRSSI(target.rssi)}</span>
            <span>{t.recording}</span>
          </div>
        </div>
        <button className="screen-fpv-video-modal__close" type="button" aria-label={t.close} onClick={onClose}>
          {closing ? <Loader2 className="app-spinner" size={18} aria-hidden="true" /> : <X size={18} aria-hidden="true" />}
        </button>
        <div className="screen-fpv-video-modal__controls">
          <button type="button" aria-label={t.fullscreen} onClick={toggleFullscreen}>
            <Maximize2 size={18} aria-hidden="true" />
          </button>
        </div>
        {status !== "ready" ? (
          <div className={status === "error" ? "screen-fpv-video-modal__overlay screen-fpv-video-modal__overlay--error" : "screen-fpv-video-modal__overlay"}>
            {status === "error" ? <Signal size={28} aria-hidden="true" /> : <Loader2 className="app-spinner" size={28} aria-hidden="true" />}
            <strong>{status === "error" ? error || t.videoError : t.videoLoading}</strong>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function renderDeviceMarker(layer: L.LayerGroup, location: ScreenDeviceLocationResponse | null, t: Record<string, string>) {
  if (!location?.valid || !validMapPoint(location.point)) {
    return;
  }

  L.marker([location.point.latitude, location.point.longitude], {
    icon: createIcon(detectionDeviceIconOnlineUrl, deviceIconSize, "screen-device-marker"),
    pane: "screenMarkers",
    riseOnHover: true,
    alt: t.deviceLocation,
  })
    .bindTooltip(deviceTooltip(location, t), {
      className: "screen-map-tooltip",
      direction: "top",
      offset: [0, -deviceIconSize[1]],
      opacity: 0.94,
    })
    .addTo(layer);
}

function renderWarningZone(layer: L.LayerGroup, zone: WarningZone, locale: Locale, t: Record<string, string>) {
  if (!validMapPoint(zone.center)) {
    return;
  }
  L.circle([zone.center.latitude, zone.center.longitude], {
    radius: zone.radiusMeters,
    color: "#f97316",
    weight: 2,
    opacity: 0.96,
    fillColor: "#f97316",
    fillOpacity: 0.12,
    dashArray: "7 7",
    pane: "screenTrajectories",
    className: "screen-map-warning-zone",
  })
    .bindTooltip(`${t.warningZone}: ${formatMeters(zone.radiusMeters, locale, t)}`, {
      className: "screen-map-tooltip",
      direction: "top",
      opacity: 0.94,
    })
    .addTo(layer);
}

function renderTargetMarker(
  layer: L.LayerGroup,
  target: ScreenPositionTarget,
  kind: "drone" | "pilot",
  selected: boolean,
  alert: boolean,
  onSelectPosition: (target: ScreenPositionTarget) => void,
  t: Record<string, string>,
) {
  const point = kind === "drone" ? target.drone : target.pilot;
  if (!validMapPoint(point)) {
    return;
  }

  const iconUrl = markerIcon(kind, selected, alert);
  const className = [
    selected ? "screen-reference-marker-selected" : "",
    alert ? "screen-reference-marker--alert" : "screen-reference-marker--whitelist",
  ].filter(Boolean).join(" ") || undefined;
  const label = kind === "drone" ? t.drone : t.pilot;
  L.marker([point.latitude, point.longitude], {
    icon: createIcon(iconUrl, targetIconSize, className),
    pane: selected ? "screenSelectedMarkers" : "screenMarkers",
    riseOnHover: true,
    alt: `${target.serial || target.id}-${kind}`,
  })
    .on("click", () => onSelectPosition(target))
    .bindTooltip(positionTooltip(target, label, point, t), {
      className: "screen-map-tooltip",
      direction: "top",
      offset: [0, -targetIconSize[1]],
      opacity: 0.94,
    })
    .addTo(layer);
}

function renderHomeMarker(
  layer: L.LayerGroup,
  target: ScreenPositionTarget,
  selected: boolean,
  onSelectPosition: (target: ScreenPositionTarget) => void,
  t: Record<string, string>,
) {
  if (!validMapPoint(target.home)) {
    return;
  }

  L.circleMarker([target.home.latitude, target.home.longitude], {
    radius: selected ? 6 : 4,
    color: "#7cdb7a",
    weight: 2,
    fillColor: "#06130a",
    fillOpacity: 0.92,
    pane: selected ? "screenSelectedMarkers" : "screenMarkers",
  })
    .on("click", () => onSelectPosition(target))
    .bindTooltip(positionTooltip(target, t.home, target.home, t), {
      className: "screen-map-tooltip",
      direction: "top",
      opacity: 0.94,
    })
    .addTo(layer);
}

function renderTrajectory(
  layer: L.LayerGroup,
  target: ScreenPositionTarget,
  kind: "drone" | "pilot",
  selected: boolean,
  onSelectPosition: (target: ScreenPositionTarget) => void,
  t: Record<string, string>,
) {
  const points = toTrackLatLngs(kind === "drone" ? target.droneTrajectory : target.pilotTrajectory);
  if (points.length < 2) {
    return;
  }

  const color = kind === "drone" ? droneTrackColor : pilotTrackColor;
  L.polyline(points, {
    color,
    weight: selected ? 4 : 2.5,
    opacity: selected ? 0.95 : 0.64,
    pane: "screenTrajectories",
    className: selected ? "screen-map-trajectory screen-map-trajectory--selected" : "screen-map-trajectory",
  })
    .on("click", () => onSelectPosition(target))
    .bindTooltip(kind === "drone" ? t.drone : t.pilot, {
      className: "screen-map-tooltip",
      direction: "top",
      opacity: 0.9,
    })
    .addTo(layer);
}

function createIcon(iconUrl: string, size: [number, number], className?: string) {
  return L.icon({
    iconUrl,
    iconSize: size,
    iconAnchor: [size[0] / 2, size[1]],
    className,
  });
}

function markerIcon(kind: "drone" | "pilot", selected: boolean, alert: boolean) {
  if (kind === "drone") {
    if (alert) {
      return selected ? selectedUavBlackFlyIconUrl : uavBlackFlyIconUrl;
    }
    return selected ? selectedUavIconUrl : uavIconUrl;
  }

  if (alert) {
    return selected ? selectedRemoteControlBlackFlyIconUrl : remoteControlBlackFlyIconUrl;
  }
  return selected ? selectedRemoteControlIconUrl : remoteControlIconUrl;
}

function mergePosition(items: ScreenPositionTarget[], target: ScreenPositionTarget, limit: number) {
  const targetSerial = normalizeTargetIdentity(target.serial);
  const targetCorrelationId = normalizeTargetIdentity(target.correlationId);
  const next = items.filter((item) => {
    if (item.id === target.id) {
      return false;
    }
    if (targetCorrelationId && normalizeTargetIdentity(item.correlationId) === targetCorrelationId) {
      return false;
    }
    if (targetSerial && normalizeTargetIdentity(item.serial) === targetSerial) {
      return false;
    }
    return true;
  });
  next.push(target);
  return sortPositions(next).slice(0, limit);
}

function removePosition(items: ScreenPositionTarget[], target: ScreenPositionTarget) {
  const targetCorrelationId = normalizeTargetIdentity(target.correlationId);
  return items.filter((item) => {
    if (item.id === target.id) {
      return false;
    }
    return !(
      targetCorrelationId &&
      normalizeTargetIdentity(item.correlationId) === targetCorrelationId &&
      isPendingEncryptedDJIDrone(item)
    );
  });
}

function normalizeTargetIdentity(value?: string) {
  return value?.trim().toLowerCase() ?? "";
}

function isPendingEncryptedDJIDrone(target: { model?: string; cracked?: boolean }) {
  return target.model?.trim().toLowerCase() === "dji-drone" && !target.cracked;
}

function mergeFPV(items: ScreenFPVTarget[], target: ScreenFPVTarget, limit: number) {
  const next = items.filter((item) => item.id !== target.id);
  next.push(target);
  return sortFPV(next).slice(0, limit);
}

function resolvePositionExpireSeconds(value: number | undefined) {
  if (
    typeof value === "number" &&
    Number.isFinite(value) &&
    value >= minPositionExpireSeconds &&
    value <= maxPositionExpireSeconds
  ) {
    return Math.floor(value);
  }
  return defaultPositionExpireSeconds;
}

function validTCPPort(value: number) {
  return Number.isInteger(value) && value >= minTCPPort && value <= maxTCPPort;
}

function resolveTCPPort(value: number | undefined, fallback: number) {
  if (validTCPPort(value ?? Number.NaN)) {
    return Math.floor(value!);
  }
  return validTCPPort(fallback) ? Math.floor(fallback) : 0;
}

function filterVisiblePositions(items: ScreenPositionTarget[], now: Date, expireSeconds: number) {
  const expireMs = resolvePositionExpireSeconds(expireSeconds) * 1000;
  return items.filter((item) => {
    const lastSeenAt = Date.parse(item.lastSeen);
    return Number.isFinite(lastSeenAt) && now.getTime() - lastSeenAt <= expireMs;
  });
}

function disposeScreenAlarmAudio(audio: HTMLAudioElement) {
  try {
    audio.pause();
    audio.muted = true;
    audio.loop = false;
    audio.currentTime = 0;
    audio.removeAttribute("src");
    audio.load();
  } catch {
    // Some browsers throw when resetting a media element mid-playback.
  }
}

function useScreenAlarmSound(active: boolean) {
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const playRequestRef = useRef(0);
  const [blocked, setBlocked] = useState(false);

  const stop = useCallback(() => {
    playRequestRef.current += 1;
    const audio = audioRef.current;
    audioRef.current = null;
    if (!audio) {
      return;
    }
    disposeScreenAlarmAudio(audio);
  }, []);

  const start = useCallback(async () => {
    const requestId = playRequestRef.current + 1;
    playRequestRef.current = requestId;
    stop();
    playRequestRef.current = requestId;
    const audio = new Audio(screenAlarmAudio);
    audio.loop = true;
    audio.preload = "auto";
    audio.volume = 0.9;
    audio.muted = false;
    audioRef.current = audio;
    try {
      await audio.play();
      if (playRequestRef.current !== requestId || audioRef.current !== audio) {
        disposeScreenAlarmAudio(audio);
        return;
      }
      setBlocked(false);
    } catch {
      if (playRequestRef.current === requestId) {
        stop();
        setBlocked(true);
      } else {
        disposeScreenAlarmAudio(audio);
      }
    }
  }, [stop]);

  useEffect(() => {
    if (!active) {
      stop();
      setBlocked(false);
      return;
    }
    void start();
    return stop;
  }, [active, start, stop]);

  useEffect(() => {
    return () => {
      stop();
      audioRef.current = null;
    };
  }, [stop]);

  return { blocked: active && blocked, enable: start };
}

function resolveDisplayModel(record: Pick<IntrusionRecord, "displayModel" | "model"> | Pick<ScreenPositionTarget, "model"> | null | undefined) {
  if (!record) {
    return "";
  }
  if ("displayModel" in record) {
    const displayModel = record.displayModel?.trim();
    if (displayModel) {
      return displayModel;
    }
  }
  return record.model?.trim() ?? "";
}

function intrusionToPositionTarget(record: IntrusionRecord): ScreenPositionTarget {
  return {
    id: record.targetId || record.id,
    serial: record.serial ?? "",
    model: resolveDisplayModel(record) || record.model || "",
    source: record.source ?? "intrusion",
    sources: record.sources,
    frequency: record.frequency,
    rssi: record.rssi,
    device: record.device,
    drone: record.drone,
    pilot: record.pilot,
    home: record.home,
    droneTrajectory: record.droneTrajectory,
    pilotTrajectory: record.pilotTrajectory,
    height: record.height,
    altitude: record.altitude,
    speed: record.speed,
    pilotDistanceM: record.pilotDistanceM,
    droneDistanceM: record.droneDistanceM,
    droneDirectionDeg: record.droneDirectionDeg,
    firstSeen: record.firstSeen,
    lastSeen: record.lastSeen,
    hitCount: record.hitCount,
    cracked: record.cracked,
    lastRecord: record.lastRecord ?? {
      type: record.source ?? "intrusion",
      receivedAt: record.lastSeen,
      device: record.device,
      serial: record.serial,
      model: record.model,
      frequency: record.frequency,
      rssi: record.rssi,
      cracked: record.cracked,
    },
  };
}

function hasIntrusionMapData(record: IntrusionRecord) {
  if (
    validMapPoint(record.deviceLocation?.point) ||
    validMapPoint(record.drone) ||
    validMapPoint(record.pilot) ||
    validMapPoint(record.home)
  ) {
    return true;
  }
  return toTrackLatLngs(record.droneTrajectory).length > 0 || toTrackLatLngs(record.pilotTrajectory).length > 0;
}

function intrusionTrackPointCount(record: IntrusionRecord) {
  return toTrackLatLngs(record.droneTrajectory).length + toTrackLatLngs(record.pilotTrajectory).length;
}

function intrusionCoordinateParts(record: IntrusionRecord, t: Record<string, string>) {
  const parts: Array<{ key: string; label: string; value: string }> = [];
  if (presentCoordinatePoint(record.deviceLocation?.point)) {
    parts.push({ key: "device", label: t.deviceLocation, value: formatPoint(record.deviceLocation.point) });
  }
  if (presentCoordinatePoint(record.drone)) {
    parts.push({ key: "drone", label: t.drone, value: formatPoint(record.drone) });
  }
  if (presentCoordinatePoint(record.pilot)) {
    parts.push({ key: "pilot", label: t.pilot, value: formatPoint(record.pilot) });
  }
  if (presentCoordinatePoint(record.home)) {
    parts.push({ key: "home", label: t.home, value: formatPoint(record.home) });
  }
  return parts;
}

function defaultUserSettings(): UserSettings {
  return {
    intrusionRetentionDays: 90,
    positionExpireSeconds: defaultPositionExpireSeconds,
    positionTCPPort: undefined,
    fpvTCPPort: undefined,
    screenTitle: "",
    screenStrikeChannelLabels: defaultStrikeChannelLabels(),
    warningZoneEnabled: false,
    warningZoneRadiusMeters: defaultWarningZoneRadiusMeters,
    whitelist: [],
  };
}

function resolveUserSettings(settings?: UserSettings | null): UserSettings {
  return {
    intrusionRetentionDays: settings?.intrusionRetentionDays ?? 90,
    positionExpireSeconds: resolvePositionExpireSeconds(settings?.positionExpireSeconds),
    positionTCPPort: settings?.positionTCPPort,
    fpvTCPPort: settings?.fpvTCPPort,
    screenTitle: settings?.screenTitle ?? "",
    screenStrikeChannelLabels: normalizeScreenStrikeChannelLabels(settings?.screenStrikeChannelLabels),
    warningZoneEnabled: resolveWarningZoneEnabled(settings),
    warningZoneRadiusMeters: resolveWarningZoneRadiusMeters(settings),
    whitelist: settings?.whitelist ?? [],
  };
}

function resolveWarningZoneEnabled(settings?: UserSettings | null) {
  if (typeof settings?.warningZoneEnabled === "boolean") {
    return settings.warningZoneEnabled;
  }
  return Boolean(settings?.warningZones?.length);
}

function resolveWarningZoneRadiusMeters(settings?: UserSettings | null) {
  const candidates = [
    settings?.warningZoneRadiusMeters,
    settings?.warningZones?.[0]?.radiusMeters,
    defaultWarningZoneRadiusMeters,
  ];
  const radius = candidates.find((value) => (
    typeof value === "number" &&
    Number.isFinite(value) &&
    value >= minWarningZoneRadiusMeters &&
    value <= maxWarningZoneRadiusMeters
  ));
  return Math.round(radius ?? defaultWarningZoneRadiusMeters);
}

function resolveActiveWarningZone(settings: UserSettings, deviceLocation: ScreenDeviceLocationResponse | null): WarningZone | null {
  if (!resolveWarningZoneEnabled(settings) || !deviceLocation?.valid || !validMapPoint(deviceLocation.point)) {
    return null;
  }
  return {
    id: "device-warning-zone",
    center: deviceLocation.point,
    radiusMeters: resolveWarningZoneRadiusMeters(settings),
  };
}

function normalizeWhitelistSerial(serial: string | undefined | null) {
  return (serial ?? "").trim().toLowerCase();
}

function isSerialWhitelisted(serial: string | undefined | null, whitelist: WhitelistItem[] | undefined) {
  const normalized = normalizeWhitelistSerial(serial);
  if (!normalized) {
    return false;
  }
  return Boolean(whitelist?.some((item) => normalizeWhitelistSerial(item.serial) === normalized));
}

function countAlarmPositions(
  positions: ScreenPositionTarget[],
  whitelist: WhitelistItem[] | undefined,
  warningZone: WarningZone | null,
) {
  return positions.reduce((count, target) => {
    return count + (targetTriggersAlarm(target, isSerialWhitelisted(target.serial, whitelist), warningZone) ? 1 : 0);
  }, 0);
}

function targetTriggersAlarm(target: ScreenPositionTarget, whitelisted: boolean, warningZone: WarningZone | null) {
  if (whitelisted) {
    return false;
  }
  if (!warningZone) {
    return true;
  }
  return targetInsideWarningZone(target, warningZone);
}

function targetInsideWarningZone(target: ScreenPositionTarget, zone: WarningZone) {
  if (!validMapPoint(target.drone)) {
    return false;
  }
  const point = L.latLng(target.drone.latitude, target.drone.longitude);
  return point.distanceTo(L.latLng(zone.center.latitude, zone.center.longitude)) <= zone.radiusMeters;
}

function getStoredSoundAlarmEnabled() {
  if (typeof window === "undefined") {
    return true;
  }
  try {
    const value = window.localStorage.getItem(screenAlarmSoundStorageKey);
    return value === null ? true : value !== "false";
  } catch {
    return true;
  }
}

function persistSoundAlarmEnabled(enabled: boolean) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(screenAlarmSoundStorageKey, enabled ? "true" : "false");
  } catch {
    // Ignore storage failures; the current in-memory setting still applies.
  }
}

function upsertWhitelistItem(
  whitelist: WhitelistItem[] | undefined,
  item: Pick<WhitelistItem, "serial"> & Partial<WhitelistItem>,
) {
  const serial = item.serial.trim();
  const key = normalizeWhitelistSerial(serial);
  if (!key) {
    return whitelist ?? [];
  }
  const nextItem: WhitelistItem = {
    serial,
    model: item.model?.trim() || undefined,
    source: item.source?.trim() || undefined,
    createdAt: item.createdAt || new Date().toISOString(),
  };
  const items = whitelist ?? [];
  const index = items.findIndex((current) => normalizeWhitelistSerial(current.serial) === key);
  if (index < 0) {
    return [...items, nextItem];
  }
  const next = [...items];
  next[index] = {
    ...next[index],
    ...nextItem,
    createdAt: next[index].createdAt || nextItem.createdAt,
  };
  return next;
}

function removeWhitelistSerial(whitelist: WhitelistItem[] | undefined, serial: string | undefined | null) {
  const key = normalizeWhitelistSerial(serial);
  if (!key) {
    return whitelist ?? [];
  }
  return (whitelist ?? []).filter((item) => normalizeWhitelistSerial(item.serial) !== key);
}

function updateWhitelistItem(
  whitelist: WhitelistItem[] | undefined,
  currentSerial: string | undefined | null,
  item: Pick<WhitelistItem, "serial"> & Partial<WhitelistItem>,
) {
  const currentKey = normalizeWhitelistSerial(currentSerial);
  const serial = item.serial.trim();
  const nextKey = normalizeWhitelistSerial(serial);
  if (!currentKey || !nextKey) {
    return whitelist ?? [];
  }
  const items = whitelist ?? [];
  const currentIndex = items.findIndex((current) => normalizeWhitelistSerial(current.serial) === currentKey);
  if (currentIndex < 0) {
    return items;
  }
  const updatedItem: WhitelistItem = {
    ...items[currentIndex],
    serial,
    model: item.model?.trim() || undefined,
    source: item.source?.trim() || items[currentIndex].source,
    createdAt: items[currentIndex].createdAt || item.createdAt || new Date().toISOString(),
  };
  const duplicateIndex = items.findIndex((current, index) => (
    index !== currentIndex &&
    normalizeWhitelistSerial(current.serial) === nextKey
  ));
  if (duplicateIndex < 0) {
    const next = [...items];
    next[currentIndex] = updatedItem;
    return next;
  }
  const next = items.filter((_, index) => index !== currentIndex);
  const adjustedDuplicateIndex = duplicateIndex > currentIndex ? duplicateIndex - 1 : duplicateIndex;
  next[adjustedDuplicateIndex] = {
    ...next[adjustedDuplicateIndex],
    ...updatedItem,
    createdAt: next[adjustedDuplicateIndex].createdAt || updatedItem.createdAt,
  };
  return next;
}

function appendIntrusionRecords(current: IntrusionRecord[], incoming: IntrusionRecord[]) {
  const seen = new Set(current.map((record) => record.id));
  const next = [...current];
  incoming.forEach((record) => {
    if (!seen.has(record.id)) {
      seen.add(record.id);
      next.push(record);
    }
  });
  return next;
}

function appendFPVVideoRecords(current: FPVVideoRecord[], incoming: FPVVideoRecord[]) {
  const seen = new Set(current.map((record) => record.id));
  const next = [...current];
  incoming.forEach((record) => {
    if (!seen.has(record.id)) {
      seen.add(record.id);
      next.push(record);
    }
  });
  return next;
}

function appendInterferenceReports(current: InterferenceReportSummary[], incoming: InterferenceReportSummary[]) {
  const seen = new Set(current.map((record) => record.id));
  const next = [...current];
  incoming.forEach((record) => {
    if (!seen.has(record.id)) {
      seen.add(record.id);
      next.push(record);
    }
  });
  return next;
}

async function fetchAllIntrusions(query: IntrusionQuery) {
  const pageSize = 500;
  let offset = 0;
  let items: IntrusionRecord[] = [];
  for (;;) {
    const response = await getIntrusions(pageSize, offset, query);
    items = appendIntrusionRecords(items, response.items);
    if (!response.hasMore) {
      return items;
    }
    offset = response.nextOffset ?? offset + response.items.length;
    if (!response.items.length) {
      return items;
    }
  }
}

async function fetchAllInterferenceReports(query: InterferenceReportQuery) {
  const pageSize = 500;
  let offset = 0;
  let items: InterferenceReportSummary[] = [];
  for (;;) {
    const response = await getInterferenceReports(pageSize, offset, query);
    items = appendInterferenceReports(items, response.items);
    if (!response.hasMore) {
      return items;
    }
    offset = response.nextOffset ?? offset + response.items.length;
    if (!response.items.length) {
      return items;
    }
  }
}

function intrusionRecordsToCSV(records: IntrusionRecord[], t: Record<string, string>, locale: Locale) {
  return toCSV([
    [
      t.model,
      t.serial,
      t.frequency,
      t.rssi,
      t.firstSeen,
      t.lastSeen,
      t.duration,
      t.deviceLocation,
      t.drone,
      t.pilot,
      t.home,
      t.pilotDistance,
      t.droneDistance,
      t.speed,
      t.height,
      t.archivedAt,
    ],
    ...records.map((record) => [
      resolveDisplayModel(record) || record.model || "",
      record.serial || "",
      formatFrequency(record.frequency),
      formatRSSI(record.rssi),
      formatFullTime(record.firstSeen, locale),
      formatFullTime(record.lastSeen, locale),
      formatDuration(record.durationSeconds),
      formatPointForReport(record.deviceLocation?.point),
      formatPointForReport(record.drone),
      formatPointForReport(record.pilot),
      formatPointForReport(record.home),
      formatMeters(record.pilotDistanceM, locale, t),
      formatMeters(record.droneDistanceM, locale, t),
      formatSpeed(record.speed, locale, t),
      formatMeters(record.height, locale, t),
      formatFullTime(record.archivedAt, locale),
    ]),
  ]);
}

function intrusionTrajectoryPointRows(records: IntrusionRecord[], t: Record<string, string>, locale: Locale) {
  const rows: CSVCell[][] = [];
  records.forEach((record) => {
    appendIntrusionTrajectoryPointRows(rows, record, record.droneTrajectory, t.trajectory, locale);
    appendIntrusionTrajectoryPointRows(rows, record, record.pilotTrajectory, t.pilotTrajectory, locale);
  });
  return rows;
}

function appendIntrusionTrajectoryPointRows(
  rows: CSVCell[][],
  record: IntrusionRecord,
  points: ScreenPositionTrackPoint[] | undefined,
  trackType: string,
  locale: Locale,
) {
  points?.filter(validTrackPoint).forEach((point, index) => {
    rows.push([
      record.id,
      record.targetId || "",
      resolveDisplayModel(record) || record.model || "",
      record.serial || "",
      trackType,
      index + 1,
      formatFullTime(point.time, locale),
      point.latitude.toFixed(6),
      point.longitude.toFixed(6),
      formatCSVNumber(point.speed, 1),
      formatCSVNumber(point.height, 1),
      formatFullTime(record.firstSeen, locale),
      formatFullTime(record.lastSeen, locale),
      formatFullTime(record.archivedAt, locale),
    ]);
  });
}

function intrusionTrajectoryPointsToCSV(rows: CSVCell[][], t: Record<string, string>) {
  return toCSV([
    [
      t.recordId,
      t.targetId,
      t.model,
      t.serial,
      t.trackType,
      t.pointIndex,
      t.trackPointTime,
      t.latitude,
      t.longitude,
      t.speedMetersPerSecond,
      t.heightMeters,
      t.firstSeen,
      t.lastSeen,
      t.archivedAt,
    ],
    ...rows,
  ]);
}

function interferenceReportsToCSV(
  reports: InterferenceReportSummary[],
  t: Record<string, string>,
  locale: Locale,
  channelLabels: string[],
) {
  return toCSV([
    [
      t.status,
      t.firstSeen,
      t.lastSeen,
      t.duration,
      t.interferenceReportChannels,
      t.interferenceReportRequestedDuration,
      t.interferenceReportError,
      t.createdAt,
    ],
    ...reports.map((report) => [
      interferenceReportStatusLabel(report.status, t),
      formatFullTime(report.startedAt, locale),
      formatFullTime(report.endedAt, locale),
      formatDuration(report.durationSeconds),
      formatInterferenceReportChannels(report, channelLabels),
      formatDuration(report.requestedDurationSeconds),
      report.lastError || report.abnormalReason || "",
      formatFullTime(report.createdAt, locale),
    ]),
  ]);
}

function toCSV(rows: CSVCell[][]) {
  return rows.map((row) => row.map(csvCell).join(",")).join("\r\n");
}

function csvCell(value: CSVCell) {
  const text = String(value ?? "");
  if (/[",\r\n]/.test(text)) {
    return `"${text.replace(/"/g, "\"\"")}"`;
  }
  return text;
}

function downloadCSV(fileName: string, csv: string) {
  const blob = new Blob([`\uFEFF${csv}`], { type: "text/csv;charset=utf-8" });
  downloadBlob(fileName, blob);
}

function downloadBlob(fileName: string, blob: Blob) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = fileName;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

function reportTimestamp() {
  return new Date().toISOString().replace(/[-:]/g, "").replace(/\.\d{3}Z$/, "");
}

function reportFileName(kind: string, stamp = reportTimestamp()) {
  return `${kind}_${stamp}.csv`;
}

function archiveFileName(kind: string, stamp = reportTimestamp()) {
  return `${kind}_${stamp}.zip`;
}

const defaultStrikeChannelIDs = ["io1", "io2", "io3", "io4", "io5", "io6", "io7", "io8"];
const defaultStrikeLabels = ["433M", "915M", "1.2G", "1.4G", "1.5G", "2.4G", "5.2G", "5.8G"];

function defaultStrikeChannelLabels() {
  return [...defaultStrikeLabels];
}

function defaultStrikeChannelLabel(index: number) {
  return defaultStrikeChannelLabels()[index] ?? `IO${index + 1}`;
}

function normalizeScreenStrikeChannelLabels(labels?: string[] | null) {
  const normalized = defaultStrikeChannelLabels().map((_, index) => (labels?.[index] ?? "").trim().slice(0, 32));
  return normalized;
}

function formatStrikeBand(value: string) {
  const trimmed = value.trim();
  if (!trimmed) {
    return "";
  }
  const numeric = Number(trimmed);
  if (Number.isFinite(numeric)) {
    return `${trimmed}${numeric >= 100 ? "M" : "G"}`;
  }
  return trimmed;
}

function formatStrikeChannelBands(channel: InterferenceChannel) {
  const bands = channel.bands ?? [];
  return bands.map(formatStrikeBand).filter(Boolean).join("/");
}

function formatStrikeChannelLabel(channel: InterferenceChannel, index: number, customLabels: string[]) {
  return customLabels[index]?.trim() || formatStrikeChannelBands(channel) || channel.label || channel.id;
}

function formatStrikeChannelTitle(channel: InterferenceChannel, index: number, customLabels: string[]) {
  const label = formatStrikeChannelLabel(channel, index, customLabels);
  const parts = [label, channel.label, `Y${channel.output}`].filter(Boolean);
  return Array.from(new Set(parts)).join(" / ");
}

function clampStrikeDuration(value: number) {
  if (!Number.isFinite(value)) {
    return screenStrikeDefaultDurationSeconds;
  }
  return Math.max(screenStrikeMinDurationSeconds, Math.min(screenStrikeMaxDurationSeconds, Math.round(value)));
}

function getStrikeRemainingSeconds(state: ScreenStrikeState | null, now: Date, syncedAt: number) {
  if (!state?.active) {
    return 0;
  }
  const remainingSeconds = Math.max(0, state.remainingSeconds ?? 0);
  const elapsedSeconds = Math.max(0, Math.floor((now.getTime() - syncedAt) / 1000));
  return Math.max(0, remainingSeconds - elapsedSeconds);
}

function formatDateKey(value: string | undefined) {
  const time = value ? Date.parse(value) : Number.NaN;
  if (!Number.isFinite(time)) {
    return "";
  }
  const date = new Date(time);
  return [
    date.getFullYear(),
    String(date.getMonth() + 1).padStart(2, "0"),
    String(date.getDate()).padStart(2, "0"),
  ].join("-");
}

function interferenceReportStatusLabel(status: InterferenceReportStatus, t: Record<string, string>) {
  switch (status) {
    case "running":
      return t.interferenceReportStatusRunning;
    case "completed":
      return t.interferenceReportStatusCompleted;
    case "failed":
      return t.interferenceReportStatusFailed;
    case "abnormal":
      return t.interferenceReportStatusAbnormal;
    default:
      return status || t.unknown;
  }
}

function formatInterferenceReportChannels(report: InterferenceReportSummary, customLabels: string[]) {
  const ids = report.channelIds ?? [];
  const labels = report.channelLabels ?? [];
  const values = (labels.length ? labels : ids).map((value, index) => {
    const channelID = ids[index] ?? value;
    const customIndex = defaultStrikeChannelIDs.indexOf(channelID);
    return customIndex >= 0 ? customLabels[customIndex] || value : value;
  }).map((value) => value.trim()).filter(Boolean);
  return values.length ? values.join(", ") : "-";
}

function filterVisibleFPV(items: ScreenFPVTarget[], now: Date) {
  return items.filter((item) => {
    const lastSeenAt = Date.parse(item.lastSeen);
    return Number.isFinite(lastSeenAt) && now.getTime() - lastSeenAt <= fpvTargetExpireMs;
  });
}

function sortPositions(items: ScreenPositionTarget[]) {
  return [...items].sort((a, b) => {
    const firstSeenDelta = Date.parse(b.firstSeen) - Date.parse(a.firstSeen);
    if (firstSeenDelta !== 0) {
      return firstSeenDelta;
    }
    return Date.parse(b.lastSeen) - Date.parse(a.lastSeen);
  });
}

function sortFPV(items: ScreenFPVTarget[]) {
  return [...items].sort((a, b) => Date.parse(b.lastSeen) - Date.parse(a.lastSeen));
}

function presentCoordinatePoint(point?: GeoPoint | ScreenPositionPoint | null): point is ScreenPositionPoint {
  return Boolean(
    point &&
      Number.isFinite(point.latitude) &&
      Number.isFinite(point.longitude),
  );
}

function validMapPoint(point?: GeoPoint | ScreenPositionPoint | null): point is ScreenPositionPoint {
  return Boolean(
    point &&
      Number.isFinite(point.latitude) &&
      Number.isFinite(point.longitude) &&
      point.latitude >= -90 &&
      point.latitude <= 90 &&
      point.longitude >= -180 &&
      point.longitude <= 180 &&
      !(point.latitude === 0 && point.longitude === 0),
  );
}

function formatManualCoordinate(value: number) {
  return Number.isFinite(value) ? String(value) : "";
}

function normalizeCoordinateInput(value: string) {
  return value.replace(/[^\d.,-]/g, "").replace(",", ".");
}

function parseCoordinateDraft(value: string) {
  if (value.trim() === "") {
    return Number.NaN;
  }
  return Number(value.replace(",", "."));
}

function validLatitude(value: number) {
  return Number.isFinite(value) && value >= -90 && value <= 90;
}

function validLongitude(value: number) {
  return Number.isFinite(value) && value >= -180 && value <= 180;
}

function validManualPoint(latitude: number, longitude: number) {
  return validLatitude(latitude) && validLongitude(longitude) && !(latitude === 0 && longitude === 0);
}

function getNavigationCoordinates(point: ScreenPositionPoint) {
  const converted = L.coordConverter.gps84ToGcj02(point.longitude, point.latitude);
  return {
    original: point,
    converted: {
      latitude: converted.lat,
      longitude: converted.lng,
    } satisfies ScreenPositionPoint,
  };
}

function buildNavigationUrl(coordinates: ReturnType<typeof getNavigationCoordinates>, provider: NavigationMapProvider) {
  if (provider === "google") {
    const latitude = coordinates.original.latitude.toFixed(6);
    const longitude = coordinates.original.longitude.toFixed(6);
    return `https://www.google.com/maps?q=${latitude},${longitude}`;
  }

  return `https://m.amap.com/share/index/lnglat=${coordinates.converted.longitude.toFixed(6)},${coordinates.converted.latitude.toFixed(6)}&src=mypage&callnative=1&innersrc=uriapi`;
}

async function createNavigationQRCode(
  point: ScreenPositionPoint,
  provider: (typeof navigationMapProviders)[number],
) {
  const coordinates = getNavigationCoordinates(point);
  const coordinate = provider.id === "google" ? coordinates.original : coordinates.converted;
  const coordinateSystem: NavigationCoordinateSystem = provider.id === "google" ? "WGS84" : "GCJ-02";
  const coordinateLabelKey = provider.id === "google" ? "navigationCoordinateOriginal" : "navigationCoordinateConverted";
  const url = buildNavigationUrl(coordinates, provider.id);
  const dataUrl = await QRCode.toDataURL(url, {
    errorCorrectionLevel: "M",
    margin: 1,
    width: 320,
    color: {
      dark: "#06131f",
      light: "#ffffff",
    },
  });

  return {
    provider: provider.id,
    labelKey: provider.labelKey,
    url,
    dataUrl,
    coordinate,
    coordinateSystem,
    coordinateLabelKey,
  } satisfies NavigationQRCodeItem;
}

async function createNavigationQRCodes(label: string, point: ScreenPositionPoint) {
  const coordinates = getNavigationCoordinates(point);
  const results = await Promise.allSettled(
    navigationMapProviders.map((provider) => createNavigationQRCode(point, provider)),
  );
  const items = results.map((result, index) => {
    const provider = navigationMapProviders[index];
    if (result.status === "fulfilled") {
      return result.value;
    }
    const coordinate = provider.id === "google" ? coordinates.original : coordinates.converted;
    const coordinateSystem: NavigationCoordinateSystem = provider.id === "google" ? "WGS84" : "GCJ-02";
    const coordinateLabelKey = provider.id === "google" ? "navigationCoordinateOriginal" : "navigationCoordinateConverted";
    return {
      provider: provider.id,
      labelKey: provider.labelKey,
      url: buildNavigationUrl(coordinates, provider.id),
      dataUrl: "",
      coordinate,
      coordinateSystem,
      coordinateLabelKey,
    } satisfies NavigationQRCodeItem;
  });

  return {
    label,
    point: coordinates.original,
    convertedPoint: coordinates.converted,
    items,
  } satisfies NavigationQRCodeState;
}

function validTrackPoint(point?: ScreenPositionTrackPoint | null): point is ScreenPositionTrackPoint {
  return Boolean(point && validMapPoint(point));
}

function toTrackLatLngs(points?: ScreenPositionTrackPoint[]) {
  if (!points?.length) {
    return [];
  }
  return points.filter(validTrackPoint).map((point) => L.latLng(point.latitude, point.longitude));
}

function warningZoneBoundsPoints(zone: WarningZone) {
  if (!validMapPoint(zone.center) || !Number.isFinite(zone.radiusMeters) || zone.radiusMeters <= 0) {
    return [];
  }
  const center = L.latLng(zone.center.latitude, zone.center.longitude);
  return [
    center,
    center.toBounds(zone.radiusMeters * 2).getNorthWest(),
    center.toBounds(zone.radiusMeters * 2).getSouthEast(),
  ];
}

function collectMapPoints(location: ScreenDeviceLocationResponse | null, positions: ScreenPositionTarget[], warningZone?: WarningZone | null) {
  const points: L.LatLng[] = [];
  if (location?.valid && validMapPoint(location.point)) {
    points.push(L.latLng(location.point.latitude, location.point.longitude));
  }
  positions.forEach((target) => {
    for (const point of [target.drone, target.pilot, target.home]) {
      if (validMapPoint(point)) {
        points.push(L.latLng(point.latitude, point.longitude));
      }
    }
    points.push(...toTrackLatLngs(target.droneTrajectory));
    points.push(...toTrackLatLngs(target.pilotTrajectory));
  });
  if (warningZone) {
    points.push(...warningZoneBoundsPoints(warningZone));
  }
  return points;
}

function fitBounds(map: L.Map, points: L.LatLng[]) {
  if (!points.length) {
    map.setView(referenceMapCenter, referenceMapZoom, { animate: false });
    return;
  }
  if (points.length === 1) {
    map.setView(points[0], Math.max(map.getZoom(), 14), { animate: false });
    return;
  }
  const size = map.getSize();
  map.fitBounds(L.latLngBounds(points), {
    paddingTopLeft: L.point(Math.min(112, Math.max(32, size.x * 0.1)), Math.min(120, Math.max(40, size.y * 0.16))),
    paddingBottomRight: L.point(Math.min(520, Math.max(64, size.x * 0.3)), Math.min(120, Math.max(40, size.y * 0.16))),
    maxZoom: 14,
    animate: false,
  });
}

function firstMapPoint(target: ScreenPositionTarget): ScreenPositionPoint | null {
  return target.drone ?? target.pilot ?? target.home ?? null;
}

function getPositionDroneImageUrl(model: string) {
  const name = positionModelImageNames[model.trim().toLowerCase()];
  if (name) {
    return uavImageModules[`./assets/images/uav/${name}.png`] ?? mini2Image;
  }
  return getDroneImageUrl(model);
}

function getDroneImageUrl(model: string) {
  if (!model) {
    return mini2Image;
  }
  return droneImageModules[`./assets/images/drone/${model}.png`] ?? mini2Image;
}

function formatScreenDate(value: Date) {
  const year = value.getFullYear();
  const month = String(value.getMonth() + 1).padStart(2, "0");
  const day = String(value.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function formatPoint(point: GeoPoint | ScreenPositionPoint) {
  return `${point.latitude.toFixed(6)}, ${point.longitude.toFixed(6)}`;
}

function formatOptionalPoint(point?: GeoPoint | ScreenPositionPoint | null) {
  if (!validMapPoint(point)) {
    return "-";
  }
  return formatPoint(point);
}

function formatPointForReport(point?: GeoPoint | ScreenPositionPoint | null) {
  if (!validMapPoint(point)) {
    return "";
  }
  return formatPoint(point);
}

function formatCoordinateValue(value: number | undefined) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return "-";
  }
  return value.toFixed(6);
}

function formatNavigationCoordinates(point: ScreenPositionPoint) {
  return `${formatCoordinateValue(point.latitude)}, ${formatCoordinateValue(point.longitude)}`;
}

function appendVideoSessionParam(url: string, sessionToken: string) {
  const separator = url.includes("?") ? "&" : "?";
  return `${url}${separator}session=${encodeURIComponent(sessionToken)}`;
}

function deleteWHEPResource(resourceURL: string) {
  if (!resourceURL) {
    return;
  }
  void fetch(resourceURL, { method: "DELETE", keepalive: true }).catch(() => undefined);
}

function waitForICEGatheringComplete(peer: RTCPeerConnection, timeoutMs: number) {
  if (peer.iceGatheringState === "complete") {
    return Promise.resolve();
  }
  return new Promise<void>((resolve) => {
    let settled = false;
    const done = () => {
      if (settled) {
        return;
      }
      settled = true;
      window.clearTimeout(timer);
      peer.removeEventListener("icegatheringstatechange", handleStateChange);
      resolve();
    };
    const handleStateChange = () => {
      if (peer.iceGatheringState === "complete") {
        done();
      }
    };
    const timer = window.setTimeout(done, timeoutMs);
    peer.addEventListener("icegatheringstatechange", handleStateChange);
  });
}

async function readTextOrStatus(response: Response) {
  const text = await response.text().catch(() => "");
  return text.trim() || `请求失败: ${response.status}`;
}

function formatTargetTime(value: string, locale: Locale) {
  const time = Date.parse(value);
  if (!Number.isFinite(time)) {
    return "-";
  }
  return new Date(time).toLocaleTimeString(locale, { hour12: false });
}

function formatFullTime(value: string | undefined, locale: Locale) {
  const time = value ? Date.parse(value) : Number.NaN;
  if (!Number.isFinite(time)) {
    return "-";
  }
  return new Date(time).toLocaleString(locale, { hour12: false });
}

function formatDuration(seconds: number | undefined) {
  if (typeof seconds !== "number" || !Number.isFinite(seconds) || seconds <= 0) {
    return "0s";
  }
  const safeSeconds = Math.floor(seconds);
  const minutes = Math.floor(safeSeconds / 60);
  const rest = safeSeconds % 60;
  if (minutes <= 0) {
    return `${rest}s`;
  }
  return `${minutes}m ${rest}s`;
}

function formatFileSize(bytes: number | undefined, locale: Locale) {
  if (typeof bytes !== "number" || !Number.isFinite(bytes) || bytes <= 0) {
    return "-";
  }
  const units = ["B", "KB", "MB", "GB"];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex++;
  }
  return `${value.toLocaleString(locale, {
    maximumFractionDigits: unitIndex === 0 ? 0 : 1,
  })} ${units[unitIndex]}`;
}

function formatCountdown(seconds: number) {
  const safeSeconds = Math.max(0, Math.floor(seconds));
  const minutes = Math.floor(safeSeconds / 60);
  const rest = safeSeconds % 60;
  return `${String(minutes).padStart(2, "0")}:${String(rest).padStart(2, "0")}`;
}

function targetDisappearRemainingSeconds(lastSeen: string, now: Date, expireSeconds: number) {
  const lastSeenAt = Date.parse(lastSeen);
  if (!Number.isFinite(lastSeenAt)) {
    return null;
  }
  return Math.max(0, Math.ceil(resolvePositionExpireSeconds(expireSeconds) - (now.getTime() - lastSeenAt) / 1000));
}

function getTargetTimeTone(lastSeen: string, now: Date, expireSeconds: number) {
  const lastSeenAt = Date.parse(lastSeen);
  if (!Number.isFinite(lastSeenAt)) {
    return "unknown";
  }
  const ageMs = Math.max(0, now.getTime() - lastSeenAt);
  const expireMs = resolvePositionExpireSeconds(expireSeconds) * 1000;
  const freshMs = Math.max(1000, Math.min(15_000, expireMs * 0.35));
  const staleMs = Math.max(freshMs, Math.min(40_000, expireMs * 0.75));
  if (ageMs <= freshMs) {
    return "fresh";
  }
  if (ageMs <= staleMs) {
    return "stale";
  }
  return "old";
}

function shouldShowDisappearCountdown(tone: string) {
  return tone === "old";
}

function formatFrequency(value?: number) {
  if (typeof value !== "number" || !Number.isFinite(value) || value === 0) {
    return "-";
  }
  return `${Math.round(value)}MHz`;
}

function formatRSSI(value?: number) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return "-";
  }
  return value <= 0 ? `${Math.round(value)}dBm` : value.toFixed(0);
}

function formatCSVNumber(value: number | undefined, maximumFractionDigits: number) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return "";
  }
  return value.toFixed(maximumFractionDigits).replace(/\.?0+$/, "");
}

function formatMeters(value: number | undefined, locale: Locale, t: Record<string, string>) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return "-";
  }
  const absValue = Math.abs(value);
  if (absValue >= 1000) {
    const maximumFractionDigits = absValue >= 100_000 ? 0 : 1;
    return `${(value / 1000).toLocaleString(locale, { maximumFractionDigits })}${t.kilometers}`;
  }
  return `${value.toLocaleString(locale, { maximumFractionDigits: 0 })}${t.meters}`;
}

function formatSpeed(value: number | undefined, locale: Locale, t: Record<string, string>) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return "-";
  }
  return `${value.toLocaleString(locale, { maximumFractionDigits: 1 })}${t.metersPerSecond}`;
}

function formatTemperature(value: number | undefined, label: string, locale: Locale) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return `${label}: -`;
  }
  return `${label}: ${value.toLocaleString(locale, { maximumFractionDigits: 1 })}C`;
}

function formatAge(value: string, now: Date, t: Record<string, string>) {
  const time = Date.parse(value);
  if (!Number.isFinite(time)) {
    return "-";
  }
  const seconds = Math.max(0, Math.round((now.getTime() - time) / 1000));
  if (seconds < 5) {
    return t.justNow;
  }
  if (seconds < 60) {
    return `${seconds}${t.secondsAgo}`;
  }
  return `${Math.floor(seconds / 60)}${t.minutesAgo}`;
}

function rssiPercent(value: number) {
  if (!Number.isFinite(value)) {
    return 0;
  }
  const percent = value <= 0 ? 100 + value : (value / 255) * 100;
  return Math.max(0, Math.min(100, percent));
}

function escapeHtml(value: string) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function positionTooltip(target: ScreenPositionTarget, label: string, point: ScreenPositionPoint, t: Record<string, string>) {
  return [
    `<strong>${escapeHtml(label)}</strong>`,
    escapeHtml(target.model || t.unknown),
    escapeHtml(target.serial || target.id),
    escapeHtml(formatPoint(point)),
    `${escapeHtml(t.frequency)}: ${escapeHtml(formatFrequency(target.frequency))}`,
    `${escapeHtml(t.rssi)}: ${escapeHtml(formatRSSI(target.rssi))}`,
  ].join("<br>");
}

function deviceTooltip(location: ScreenDeviceLocationResponse, t: Record<string, string>) {
  return [
    `<strong>${escapeHtml(t.deviceLocation)}</strong>`,
    location.point ? escapeHtml(formatPoint(location.point)) : "-",
    `${escapeHtml(t.time)}: ${escapeHtml(location.updatedAt ? new Date(location.updatedAt).toLocaleString() : "-")}`,
  ].join("<br>");
}
