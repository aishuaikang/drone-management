import type { ScreenPositionTrackPoint } from "../types";

const fallbackPointIntervalMs = 4_000;

export type TrackReplayKind = "drone" | "pilot";

export interface TrackReplayTimeline {
  points: ScreenPositionTrackPoint[];
  elapsedMs: number[];
  durationMs: number;
  startTimeMs: number | null;
  usesRecordedTime: boolean;
}

export interface TrackReplaySample {
  elapsedMs: number;
  fraction: number;
  height?: number;
  latitude: number;
  longitude: number;
  lowerIndex: number;
  speed?: number;
  timeMs: number | null;
  upperIndex: number;
}

function finiteMetric(value: number | undefined) {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function interpolateMetric(from: number | undefined, to: number | undefined, fraction: number) {
  const start = finiteMetric(from);
  const end = finiteMetric(to);
  if (start !== undefined && end !== undefined) {
    return start + (end - start) * fraction;
  }
  return end ?? start;
}

function clampProgress(progress: number) {
  if (!Number.isFinite(progress)) {
    return 0;
  }
  return Math.min(1, Math.max(0, progress));
}

export function selectTrackReplayKind(
  dronePointCount: number,
  pilotPointCount: number,
): TrackReplayKind {
  if (dronePointCount > 1) {
    return "drone";
  }
  if (pilotPointCount > 1) {
    return "pilot";
  }
  return dronePointCount > 0 ? "drone" : "pilot";
}

export function buildTrackReplayTimeline(points: ScreenPositionTrackPoint[]): TrackReplayTimeline {
  if (points.length === 0) {
    return {
      points: [],
      elapsedMs: [],
      durationMs: 0,
      startTimeMs: null,
      usesRecordedTime: false,
    };
  }

  const entries = points.map((point, index) => ({
    index,
    point,
    timeMs: Date.parse(point.time),
  }));
  const hasValidTimes = entries.every((entry) => Number.isFinite(entry.timeMs));

  if (hasValidTimes) {
    entries.sort((left, right) => left.timeMs - right.timeMs || left.index - right.index);
    const startTimeMs = entries[0].timeMs;
    const durationMs = entries.at(-1)!.timeMs - startTimeMs;
    if (durationMs > 0) {
      return {
        points: entries.map((entry) => entry.point),
        elapsedMs: entries.map((entry) => entry.timeMs - startTimeMs),
        durationMs,
        startTimeMs,
        usesRecordedTime: true,
      };
    }
  }

  return {
    points: [...points],
    elapsedMs: points.map((_, index) => index * fallbackPointIntervalMs),
    durationMs: Math.max(0, (points.length - 1) * fallbackPointIntervalMs),
    startTimeMs: Number.isFinite(entries[0].timeMs) ? entries[0].timeMs : null,
    usesRecordedTime: false,
  };
}

function findUpperIndex(elapsedMs: number[], targetMs: number) {
  let low = 0;
  let high = elapsedMs.length - 1;
  while (low < high) {
    const middle = Math.floor((low + high) / 2);
    if (elapsedMs[middle] < targetMs) {
      low = middle + 1;
    } else {
      high = middle;
    }
  }
  return low;
}

export function sampleTrackReplay(
  timeline: TrackReplayTimeline,
  progress: number,
): TrackReplaySample | null {
  const { points } = timeline;
  if (points.length === 0) {
    return null;
  }
  if (points.length === 1 || timeline.durationMs <= 0) {
    const point = points[0];
    return {
      elapsedMs: 0,
      fraction: 0,
      height: finiteMetric(point.height),
      latitude: point.latitude,
      longitude: point.longitude,
      lowerIndex: 0,
      speed: finiteMetric(point.speed),
      timeMs: Number.isFinite(Date.parse(point.time)) ? Date.parse(point.time) : timeline.startTimeMs,
      upperIndex: 0,
    };
  }

  const normalizedProgress = clampProgress(progress);
  const targetMs = timeline.durationMs * normalizedProgress;
  if (targetMs <= 0) {
    const point = points[0];
    return {
      elapsedMs: 0,
      fraction: 0,
      height: finiteMetric(point.height),
      latitude: point.latitude,
      longitude: point.longitude,
      lowerIndex: 0,
      speed: finiteMetric(point.speed),
      timeMs: timeline.usesRecordedTime && timeline.startTimeMs !== null
        ? timeline.startTimeMs
        : Number.isFinite(Date.parse(point.time)) ? Date.parse(point.time) : null,
      upperIndex: 0,
    };
  }
  if (targetMs >= timeline.durationMs) {
    const index = points.length - 1;
    const point = points[index];
    return {
      elapsedMs: timeline.durationMs,
      fraction: 0,
      height: finiteMetric(point.height),
      latitude: point.latitude,
      longitude: point.longitude,
      lowerIndex: index,
      speed: finiteMetric(point.speed),
      timeMs: timeline.usesRecordedTime && timeline.startTimeMs !== null
        ? timeline.startTimeMs + timeline.durationMs
        : Number.isFinite(Date.parse(point.time)) ? Date.parse(point.time) : null,
      upperIndex: index,
    };
  }

  const upperIndex = findUpperIndex(timeline.elapsedMs, targetMs);
  const lowerIndex = Math.max(0, upperIndex - 1);
  const lowerElapsed = timeline.elapsedMs[lowerIndex];
  const upperElapsed = timeline.elapsedMs[upperIndex];
  const segmentDuration = upperElapsed - lowerElapsed;
  const fraction = segmentDuration > 0 ? (targetMs - lowerElapsed) / segmentDuration : 1;
  const lower = points[lowerIndex];
  const upper = points[upperIndex];

  return {
    elapsedMs: targetMs,
    fraction,
    height: interpolateMetric(lower.height, upper.height, fraction),
    latitude: lower.latitude + (upper.latitude - lower.latitude) * fraction,
    longitude: lower.longitude + (upper.longitude - lower.longitude) * fraction,
    lowerIndex,
    speed: interpolateMetric(lower.speed, upper.speed, fraction),
    timeMs: timeline.usesRecordedTime && timeline.startTimeMs !== null
      ? timeline.startTimeMs + targetMs
      : Number.isFinite(Date.parse(upper.time)) ? Date.parse(upper.time) : null,
    upperIndex,
  };
}
