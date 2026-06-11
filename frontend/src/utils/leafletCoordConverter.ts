import L from "leaflet";

export type CoordFunction =
  | "bd09ToGps84"
  | "gps84ToBd09"
  | "gps84ToGcj02"
  | "gcj02ToGps84"
  | "gcj02ToBd09"
  | "bd09ToGcj02";

export type CoordPoint = {
  lng: number;
  lat: number;
};

type CoordType = "gps84" | "gcj02" | "bd09";

type CoordLayer = L.GridLayer & {
  options: L.GridLayerOptions & {
    coordFunction?: CoordFunction;
  };
};

type GridLayerWithInternals = CoordLayer & {
  _map: L.Map & {
    _getNewPixelOrigin(center: L.LatLngExpression, zoom: number): L.Point;
    _animatingZoom?: boolean;
    _animateToZoom?: number;
  };
  _tileZoom: number;
};

type GridLevel = {
  zoom: number;
  origin: L.Point;
  el: HTMLElement;
};

declare module "leaflet" {
  class CoordConverter {
    bd09ToGps84(lng: number, lat: number): CoordPoint;
    gps84ToBd09(lng: number, lat: number): CoordPoint;
    gps84ToGcj02(lng: number, lat: number): CoordPoint;
    gcj02ToGps84(lng: number, lat: number): CoordPoint;
    gcj02ToBd09(lng: number, lat: number): CoordPoint;
    bd09ToGcj02(lng: number, lat: number): CoordPoint;
    convertArray(coords: CoordPoint[], fromType: CoordType, toType: CoordType): CoordPoint[];
  }

  const coordConverter: CoordConverter;
  function coordConvert(): CoordConverter;

  interface TileLayerOptions {
    coordFunction?: CoordFunction;
  }

  interface GridLayerOptions {
    coordFunction?: CoordFunction;
  }
}

let installed = false;

class LeafletCoordConverter {
  private readonly pi = Math.PI;
  private readonly a = 6378245.0;
  private readonly ee = 0.006693421622965943;
  private readonly xPi = (this.pi * 3000.0) / 180.0;

  bd09ToGps84(lng: number, lat: number): CoordPoint {
    const gcj02 = this.bd09ToGcj02(lng, lat);
    return this.gcj02ToGps84(gcj02.lng, gcj02.lat);
  }

  gps84ToBd09(lng: number, lat: number): CoordPoint {
    const gcj02 = this.gps84ToGcj02(lng, lat);
    return this.gcj02ToBd09(gcj02.lng, gcj02.lat);
  }

  gps84ToGcj02(lng: number, lat: number): CoordPoint {
    if (this.outOfChina(lng, lat)) {
      return { lng, lat };
    }
    let dLat = this.transformLat(lng - 105.0, lat - 35.0);
    let dLng = this.transformLng(lng - 105.0, lat - 35.0);
    const radLat = (lat / 180.0) * this.pi;
    let magic = Math.sin(radLat);
    magic = 1 - this.ee * magic * magic;
    const sqrtMagic = Math.sqrt(magic);
    dLat = (dLat * 180.0) / (((this.a * (1 - this.ee)) / (magic * sqrtMagic)) * this.pi);
    dLng = (dLng * 180.0) / ((this.a / sqrtMagic) * Math.cos(radLat) * this.pi);
    return {
      lng: lng + dLng,
      lat: lat + dLat,
    };
  }

  gcj02ToGps84(lng: number, lat: number): CoordPoint {
    if (this.outOfChina(lng, lat)) {
      return { lng, lat };
    }
    const coord = this.gps84ToGcj02(lng, lat);
    return {
      lng: lng * 2 - coord.lng,
      lat: lat * 2 - coord.lat,
    };
  }

  gcj02ToBd09(lng: number, lat: number): CoordPoint {
    const z = Math.sqrt(lng * lng + lat * lat) + 0.00002 * Math.sin(lat * this.xPi);
    const theta = Math.atan2(lat, lng) + 0.000003 * Math.cos(lng * this.xPi);
    return {
      lng: z * Math.cos(theta) + 0.0065,
      lat: z * Math.sin(theta) + 0.006,
    };
  }

  bd09ToGcj02(lng: number, lat: number): CoordPoint {
    const x = lng - 0.0065;
    const y = lat - 0.006;
    const z = Math.sqrt(x * x + y * y) - 0.00002 * Math.sin(y * this.xPi);
    const theta = Math.atan2(y, x) - 0.000003 * Math.cos(x * this.xPi);
    return {
      lng: z * Math.cos(theta),
      lat: z * Math.sin(theta),
    };
  }

  convertArray(coords: CoordPoint[], fromType: CoordType, toType: CoordType): CoordPoint[] {
    if (fromType === toType) {
      return coords;
    }

    const methodMap: Record<string, (lng: number, lat: number) => CoordPoint> = {
      gps84_gcj02: this.gps84ToGcj02.bind(this),
      gps84_bd09: this.gps84ToBd09.bind(this),
      gcj02_gps84: this.gcj02ToGps84.bind(this),
      gcj02_bd09: this.gcj02ToBd09.bind(this),
      bd09_gps84: this.bd09ToGps84.bind(this),
      bd09_gcj02: this.bd09ToGcj02.bind(this),
    };

    const convertMethod = methodMap[`${fromType}_${toType}`];
    if (!convertMethod) {
      throw new Error(`unsupported coordinate conversion ${fromType} -> ${toType}`);
    }

    return coords.map((coord) => convertMethod(coord.lng, coord.lat));
  }

  private outOfChina(lng: number, lat: number) {
    return lng < 72.004 || lng > 137.8347 || lat < 0.8293 || lat > 55.8271;
  }

  private transformLat(x: number, y: number) {
    let result = -100.0 + 2.0 * x + 3.0 * y + 0.2 * y * y + 0.1 * x * y + 0.2 * Math.sqrt(Math.abs(x));
    result += ((20.0 * Math.sin(6.0 * x * this.pi) + 20.0 * Math.sin(2.0 * x * this.pi)) * 2.0) / 3.0;
    result += ((20.0 * Math.sin(y * this.pi) + 40.0 * Math.sin((y / 3.0) * this.pi)) * 2.0) / 3.0;
    result += ((160.0 * Math.sin((y / 12.0) * this.pi) + 320 * Math.sin((y * this.pi) / 30.0)) * 2.0) / 3.0;
    return result;
  }

  private transformLng(x: number, y: number) {
    let result = 300.0 + x + 2.0 * y + 0.1 * x * x + 0.1 * x * y + 0.1 * Math.sqrt(Math.abs(x));
    result += ((20.0 * Math.sin(6.0 * x * this.pi) + 20.0 * Math.sin(2.0 * x * this.pi)) * 2.0) / 3.0;
    result += ((20.0 * Math.sin(x * this.pi) + 40.0 * Math.sin((x / 3.0) * this.pi)) * 2.0) / 3.0;
    result += ((150.0 * Math.sin((x / 12.0) * this.pi) + 300.0 * Math.sin((x / 30.0) * this.pi)) * 2.0) / 3.0;
    return result;
  }
}

function convertCenter(layer: CoordLayer, center: L.LatLng) {
  if (!center || !layer.options) {
    return center;
  }

  switch (layer.options.coordFunction) {
    case "bd09ToGps84":
      return L.latLng(L.coordConverter.bd09ToGps84(center.lng, center.lat));
    case "gps84ToBd09":
      return L.latLng(L.coordConverter.gps84ToBd09(center.lng, center.lat));
    case "gps84ToGcj02":
      return L.latLng(L.coordConverter.gps84ToGcj02(center.lng, center.lat));
    case "gcj02ToGps84":
      return L.latLng(L.coordConverter.gcj02ToGps84(center.lng, center.lat));
    case "gcj02ToBd09":
      return L.latLng(L.coordConverter.gcj02ToBd09(center.lng, center.lat));
    case "bd09ToGcj02":
      return L.latLng(L.coordConverter.bd09ToGcj02(center.lng, center.lat));
    default:
      return center;
  }
}

export function installLeafletCoordConverter() {
  if (installed) {
    return;
  }
  installed = true;

  const leaflet = L as typeof L & {
    CoordConverter: new () => L.CoordConverter;
    coordConverter: L.CoordConverter;
    coordConvert: () => L.CoordConverter;
  };

  leaflet.CoordConverter = LeafletCoordConverter;
  leaflet.coordConverter = new leaflet.CoordConverter();
  leaflet.coordConvert = () => leaflet.coordConverter;

  L.GridLayer.include({
    _setZoomTransform(this: GridLayerWithInternals, level: GridLevel, center: L.LatLng, zoom: number) {
      const convertedCenter = convertCenter(this, center);
      const scale = this._map.getZoomScale(zoom, level.zoom);
      const translate = level.origin.multiplyBy(scale).subtract(this._map._getNewPixelOrigin(convertedCenter, zoom)).round();

      if (L.Browser.any3d) {
        L.DomUtil.setTransform(level.el, translate, scale);
      } else {
        L.DomUtil.setPosition(level.el, translate);
      }
    },

    _getTiledPixelBounds(this: GridLayerWithInternals, center: L.LatLng) {
      const convertedCenter = convertCenter(this, center);
      const map = this._map;
      const mapZoom = map._animatingZoom ? Math.max(this._map._animateToZoom ?? map.getZoom(), map.getZoom()) : map.getZoom();
      const scale = map.getZoomScale(mapZoom, this._tileZoom);
      const pixelCenter = map.project(convertedCenter, this._tileZoom).floor();
      const halfSize = map.getSize().divideBy(scale * 2);

      return new L.Bounds(pixelCenter.subtract(halfSize), pixelCenter.add(halfSize));
    },
  });
}
