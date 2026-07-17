package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"time"

	"drone-management/internal/model"
)

const (
	mapTileProxyUserAgent   = "drone-management/map-tile-proxy"
	mapTileCacheControl     = "public, max-age=86400"
	mapTileLicenseStatusTTL = 5 * time.Second
)

type mapTileUpstreams struct {
	AMapRoad      url.URL
	AMapSatellite url.URL
	Google        url.URL
}

var defaultMapTileUpstreams = mapTileUpstreams{
	AMapRoad: url.URL{
		Scheme: "https",
		Host:   "webrd04.is.autonavi.com",
		Path:   "/appmaptile",
	},
	AMapSatellite: url.URL{
		Scheme: "https",
		Host:   "webst01.is.autonavi.com",
		Path:   "/appmaptile",
	},
	Google: url.URL{
		Scheme: "https",
		Host:   "mt1.google.com",
		Path:   "/vt",
	},
}

type mapTileProxySet struct {
	amapRoad      *mapTileProxy
	amapSatellite *mapTileProxy
	google        *mapTileProxy
}

type mapTileProxy struct {
	proxy          *httputil.ReverseProxy
	normalizeQuery func(url.Values) (url.Values, error)
}

type mapTileLicenseStatusCache struct {
	mu         sync.Mutex
	ttl        time.Duration
	now        func() time.Time
	checked    bool
	validUntil time.Time
	status     model.LicenseInfo
	err        error
}

func newMapTileLicenseStatusCache() *mapTileLicenseStatusCache {
	return &mapTileLicenseStatusCache{
		ttl: mapTileLicenseStatusTTL,
		now: time.Now,
	}
}

func (c *mapTileLicenseStatusCache) load(
	loader func() (model.LicenseInfo, error),
) (model.LicenseInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.checked && now.Before(c.validUntil) {
		return c.status, c.err
	}
	status, err := loader()
	validUntil := now.Add(c.ttl)
	if status.Valid && status.ExpiresAt != nil && status.ExpiresAt.Before(validUntil) {
		validUntil = *status.ExpiresAt
	}
	c.checked = true
	c.validUntil = validUntil
	c.status = status
	c.err = err
	return status, err
}

func (c *mapTileLicenseStatusCache) invalidate() {
	c.mu.Lock()
	c.checked = false
	c.mu.Unlock()
}

func newMapTileProxySet(upstreams mapTileUpstreams) (*mapTileProxySet, error) {
	transport := newMapTileTransport()
	amapRoad, err := newMapTileProxy(upstreams.AMapRoad, normalizeAMapRoadQuery, transport)
	if err != nil {
		return nil, fmt.Errorf("create AMap road tile proxy: %w", err)
	}
	amapSatellite, err := newMapTileProxy(
		upstreams.AMapSatellite,
		normalizeAMapSatelliteQuery,
		transport,
	)
	if err != nil {
		return nil, fmt.Errorf("create AMap satellite tile proxy: %w", err)
	}
	google, err := newMapTileProxy(upstreams.Google, normalizeGoogleTileQuery, transport)
	if err != nil {
		return nil, fmt.Errorf("create Google tile proxy: %w", err)
	}

	return &mapTileProxySet{
		amapRoad:      amapRoad,
		amapSatellite: amapSatellite,
		google:        google,
	}, nil
}

func newMapTileTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.ExpectContinueTimeout = time.Second
	return transport
}

func newMapTileProxy(
	upstream url.URL,
	normalizeQuery func(url.Values) (url.Values, error),
	transport http.RoundTripper,
) (*mapTileProxy, error) {
	if upstream.Scheme != "http" && upstream.Scheme != "https" {
		return nil, fmt.Errorf("unsupported upstream scheme %q", upstream.Scheme)
	}
	if upstream.Host == "" || upstream.Path == "" {
		return nil, errors.New("upstream host and path are required")
	}

	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme = upstream.Scheme
			request.Out.URL.Host = upstream.Host
			request.Out.URL.Path = upstream.Path
			request.Out.URL.RawPath = ""
			request.Out.URL.RawQuery = request.In.URL.RawQuery
			request.Out.Host = upstream.Host
			request.Out.Header.Del("Authorization")
			request.Out.Header.Del("Cookie")
			request.Out.Header.Del("Origin")
			request.Out.Header.Del("Referer")
			request.Out.Header.Set("User-Agent", mapTileProxyUserAgent)
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Del("Set-Cookie")
			response.Header.Set("X-Content-Type-Options", "nosniff")
			if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
				response.Header.Set("Cache-Control", mapTileCacheControl)
			} else {
				response.Header.Del("Cache-Control")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			status := http.StatusBadGateway
			timedOut := errors.Is(err, context.DeadlineExceeded)
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				timedOut = true
			}
			if timedOut {
				status = http.StatusGatewayTimeout
			}
			http.Error(w, "map tile upstream unavailable", status)
		},
	}

	return &mapTileProxy{
		proxy:          proxy,
		normalizeQuery: normalizeQuery,
	}, nil
}

func (p *mapTileProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query, err := p.normalizeQuery(r.URL.Query())
	if err != nil {
		http.Error(w, "invalid map tile request", http.StatusBadRequest)
		return
	}
	request := r.Clone(r.Context())
	request.URL.RawQuery = query.Encode()
	p.proxy.ServeHTTP(w, request)
}

func normalizeAMapRoadQuery(query url.Values) (url.Values, error) {
	if err := validateQueryKeys(query, "lang", "size", "scale", "style", "x", "y", "z"); err != nil {
		return nil, err
	}
	if err := validateFixedQueryValue(query, "lang", "zh_cn"); err != nil {
		return nil, err
	}
	if err := validateFixedQueryValue(query, "size", "1"); err != nil {
		return nil, err
	}
	if err := validateFixedQueryValue(query, "scale", "1"); err != nil {
		return nil, err
	}
	if err := validateFixedQueryValue(query, "style", "7"); err != nil {
		return nil, err
	}

	normalized, err := normalizeTileCoordinates(query, 22)
	if err != nil {
		return nil, err
	}
	normalized.Set("lang", "zh_cn")
	normalized.Set("size", "1")
	normalized.Set("scale", "1")
	normalized.Set("style", "7")
	return normalized, nil
}

func normalizeAMapSatelliteQuery(query url.Values) (url.Values, error) {
	if err := validateQueryKeys(query, "style", "x", "y", "z"); err != nil {
		return nil, err
	}
	if err := validateFixedQueryValue(query, "style", "6"); err != nil {
		return nil, err
	}

	normalized, err := normalizeTileCoordinates(query, 16)
	if err != nil {
		return nil, err
	}
	normalized.Set("style", "6")
	return normalized, nil
}

func normalizeGoogleTileQuery(query url.Values) (url.Values, error) {
	if err := validateQueryKeys(query, "lyrs", "x", "y", "z"); err != nil {
		return nil, err
	}
	layer, err := singleQueryValue(query, "lyrs")
	if err != nil {
		return nil, err
	}
	maxZoom := 22
	switch layer {
	case "m":
	case "s":
		maxZoom = 21
	default:
		return nil, fmt.Errorf("unsupported Google map layer %q", layer)
	}

	normalized, err := normalizeTileCoordinates(query, maxZoom)
	if err != nil {
		return nil, err
	}
	normalized.Set("lyrs", layer)
	return normalized, nil
}

func validateQueryKeys(query url.Values, allowedKeys ...string) error {
	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = struct{}{}
	}
	for key := range query {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unsupported query parameter %q", key)
		}
	}
	return nil
}

func validateFixedQueryValue(query url.Values, key string, expected string) error {
	values, ok := query[key]
	if !ok {
		return nil
	}
	if len(values) != 1 || values[0] != expected {
		return fmt.Errorf("query parameter %q must equal %q", key, expected)
	}
	return nil
}

func normalizeTileCoordinates(query url.Values, maxZoom int) (url.Values, error) {
	z, err := parseTileQueryNumber(query, "z")
	if err != nil {
		return nil, err
	}
	if z > maxZoom {
		return nil, fmt.Errorf("tile zoom %d exceeds maximum %d", z, maxZoom)
	}
	x, err := parseTileQueryNumber(query, "x")
	if err != nil {
		return nil, err
	}
	y, err := parseTileQueryNumber(query, "y")
	if err != nil {
		return nil, err
	}
	tileCount := 1 << uint(z)
	if x >= tileCount || y >= tileCount {
		return nil, fmt.Errorf("tile coordinates %d,%d are outside zoom %d", x, y, z)
	}

	return url.Values{
		"x": {strconv.Itoa(x)},
		"y": {strconv.Itoa(y)},
		"z": {strconv.Itoa(z)},
	}, nil
}

func parseTileQueryNumber(query url.Values, key string) (int, error) {
	raw, err := singleQueryValue(query, key)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("query parameter %q must be a non-negative integer", key)
	}
	return value, nil
}

func singleQueryValue(query url.Values, key string) (string, error) {
	values := query[key]
	if len(values) != 1 || values[0] == "" {
		return "", fmt.Errorf("query parameter %q must contain one value", key)
	}
	return values[0], nil
}

func (s *Server) handleAMapRoadTile(w http.ResponseWriter, r *http.Request) {
	s.mapTiles.amapRoad.ServeHTTP(w, r)
}

func (s *Server) handleAMapSatelliteTile(w http.ResponseWriter, r *http.Request) {
	s.mapTiles.amapSatellite.ServeHTTP(w, r)
}

func (s *Server) handleGoogleTile(w http.ResponseWriter, r *http.Request) {
	s.mapTiles.google.ServeHTTP(w, r)
}

func (s *Server) requireMapTileLicense(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.license == nil {
			respondErrorCode(w, http.StatusServiceUnavailable, "license_unavailable", "license service is unavailable", nil)
			return
		}
		status, err := s.mapTileLicenseStatus.load(s.license.Status)
		if err != nil || !status.Valid {
			respondErrorCode(w, http.StatusForbidden, "license_required", "license required", status)
			return
		}
		next(w, r)
	}
}
