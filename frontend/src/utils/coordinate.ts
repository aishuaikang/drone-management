const originThreshold = 0.1;

export function isValidCoordinate(longitude: number, latitude: number) {
  if (!Number.isFinite(longitude) || !Number.isFinite(latitude)) {
    return false;
  }
  if (longitude < -180 || longitude > 180 || latitude < -90 || latitude > 90) {
    return false;
  }
  const nearOrigin = Math.abs(longitude) < originThreshold && Math.abs(latitude) < originThreshold;
  return !nearOrigin && longitude !== 0 && latitude !== 0;
}
