package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"drone-management/internal/license"
	"drone-management/internal/model"
	"drone-management/internal/store"
)

func TestMapTileLicenseStatusCache(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	cache := newMapTileLicenseStatusCache()
	cache.now = func() time.Time { return now }
	var calls int
	expiresAt := now.Add(time.Hour)
	loader := func() (model.LicenseInfo, error) {
		calls++
		return model.LicenseInfo{
			Valid:     true,
			ExpiresAt: &expiresAt,
		}, nil
	}

	for range 2 {
		status, err := cache.load(loader)
		if err != nil || !status.Valid {
			t.Fatalf("load() = %#v, %v, want valid status", status, err)
		}
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1 within cache TTL", calls)
	}

	now = now.Add(mapTileLicenseStatusTTL)
	if _, err := cache.load(loader); err != nil {
		t.Fatalf("load() after TTL error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want 2 after cache TTL", calls)
	}

	cache.invalidate()
	if _, err := cache.load(loader); err != nil {
		t.Fatalf("load() after invalidate error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("loader calls = %d, want 3 after invalidation", calls)
	}
}

func TestMapTileLicenseStatusCacheExpiresWithLicense(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	cache := newMapTileLicenseStatusCache()
	cache.now = func() time.Time { return now }
	var calls int
	expiresAt := now.Add(time.Second)
	loader := func() (model.LicenseInfo, error) {
		calls++
		return model.LicenseInfo{
			Valid:     true,
			ExpiresAt: &expiresAt,
		}, nil
	}

	if _, err := cache.load(loader); err != nil {
		t.Fatalf("first load() error = %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, err := cache.load(loader); err != nil {
		t.Fatalf("load() after license expiry error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want revalidation after license expiry", calls)
	}
}

func TestMapTileRoutesCacheLicenseStatus(t *testing.T) {
	deviceSN := "drone-management-001A2B3C4D5E"
	licensePath := filepath.Join(t.TempDir(), "license.lic")
	raw := generateHTTPTestLicense(t, deviceSN, 24*time.Hour, "test", time.Now())
	if err := os.WriteFile(licensePath, raw, 0o600); err != nil {
		t.Fatalf("WriteFile(license) error = %v", err)
	}
	var statusChecks atomic.Int32
	licenseSvc := license.NewService(licensePath, func() (string, error) {
		statusChecks.Add(1)
		return deviceSN, nil
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	proxies, err := newMapTileProxySet(testMapTileUpstreams(t, upstream.URL))
	if err != nil {
		t.Fatalf("newMapTileProxySet() error = %v", err)
	}
	s := newTestServer(t, store.New(10, 10))
	s.license = licenseSvc
	s.mapTiles = proxies
	s.mapTileLicenseStatus.invalidate()

	for range 2 {
		rec := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(
			rec,
			httptest.NewRequest(http.MethodGet, "/google-tile?lyrs=m&x=1&y=1&z=2", nil),
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}
	if checks := statusChecks.Load(); checks != 1 {
		t.Fatalf("license status checks = %d, want 1 for repeated tile requests", checks)
	}
}

func TestLicenseUploadInvalidatesMapTileLicenseStatus(t *testing.T) {
	deviceSN := "drone-management-001A2B3C4D5E"
	licensePath := filepath.Join(t.TempDir(), "license.lic")
	s := newTestServerWithLicense(t, licensePath, deviceSN)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	proxies, err := newMapTileProxySet(testMapTileUpstreams(t, upstream.URL))
	if err != nil {
		t.Fatalf("newMapTileProxySet() error = %v", err)
	}
	s.mapTiles = proxies

	tilePath := "/google-tile?lyrs=m&x=1&y=1&z=2"
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tilePath, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status before upload = %d, body = %s", rec.Code, rec.Body.String())
	}

	raw := generateHTTPTestLicense(t, deviceSN, 24*time.Hour, "customer", time.Now())
	upload := newMultipartRequest(t, "/api/v1/license/upload", "file", "license.lic", raw, nil)
	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, upload)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tilePath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status after upload = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestMapTileProxyRoutes(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		upstream  string
		wantQuery url.Values
	}{
		{
			name:     "AMap road",
			path:     "/amap-road-tile?lang=zh_cn&size=1&scale=1&style=7&x=4&y=5&z=3",
			upstream: "/amap-road",
			wantQuery: url.Values{
				"lang":  {"zh_cn"},
				"scale": {"1"},
				"size":  {"1"},
				"style": {"7"},
				"x":     {"4"},
				"y":     {"5"},
				"z":     {"3"},
			},
		},
		{
			name:     "AMap satellite",
			path:     "/amap-satellite-tile?style=6&x=4&y=5&z=3",
			upstream: "/amap-satellite",
			wantQuery: url.Values{
				"style": {"6"},
				"x":     {"4"},
				"y":     {"5"},
				"z":     {"3"},
			},
		},
		{
			name:     "Google road",
			path:     "/google-tile?lyrs=m&x=4&y=5&z=3",
			upstream: "/google",
			wantQuery: url.Values{
				"lyrs": {"m"},
				"x":    {"4"},
				"y":    {"5"},
				"z":    {"3"},
			},
		},
		{
			name:     "Google satellite",
			path:     "/google-tile?lyrs=s&x=4&y=5&z=3",
			upstream: "/google",
			wantQuery: url.Values{
				"lyrs": {"s"},
				"x":    {"4"},
				"y":    {"5"},
				"z":    {"3"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "private, max-age=0")
				w.Header().Set("Set-Cookie", "upstream=value")
				_ = json.NewEncoder(w).Encode(mapTileUpstreamRequest{
					Path:          r.URL.Path,
					Query:         r.URL.Query(),
					Host:          r.Host,
					UserAgent:     r.UserAgent(),
					Authorization: r.Header.Get("Authorization"),
					Cookie:        r.Header.Get("Cookie"),
					Origin:        r.Header.Get("Origin"),
					Referer:       r.Header.Get("Referer"),
				})
			}))
			defer upstream.Close()

			upstreams := testMapTileUpstreams(t, upstream.URL)
			proxies, err := newMapTileProxySet(upstreams)
			if err != nil {
				t.Fatalf("newMapTileProxySet() error = %v", err)
			}
			s := newTestServer(t, store.New(10, 10))
			s.mapTiles = proxies

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("Authorization", "Bearer secret")
			req.Header.Set("Cookie", "session=secret")
			req.Header.Set("Origin", "https://client.example")
			req.Header.Set("Referer", "https://client.example/map")
			req.Header.Set("User-Agent", "client-browser")
			rec := httptest.NewRecorder()
			s.server.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != mapTileCacheControl {
				t.Fatalf("Cache-Control = %q, want %q", got, mapTileCacheControl)
			}
			if got := rec.Header().Get("Set-Cookie"); got != "" {
				t.Fatalf("Set-Cookie = %q, want empty", got)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
			}

			var received mapTileUpstreamRequest
			if err := json.NewDecoder(rec.Body).Decode(&received); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if received.Path != tt.upstream {
				t.Fatalf("upstream path = %q, want %q", received.Path, tt.upstream)
			}
			if received.Query.Encode() != tt.wantQuery.Encode() {
				t.Fatalf("upstream query = %q, want %q", received.Query.Encode(), tt.wantQuery.Encode())
			}
			upstreamURL, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			if received.Host != upstreamURL.Host {
				t.Fatalf("upstream Host = %q, want %q", received.Host, upstreamURL.Host)
			}
			if received.UserAgent != mapTileProxyUserAgent {
				t.Fatalf("upstream User-Agent = %q, want %q", received.UserAgent, mapTileProxyUserAgent)
			}
			if received.Authorization != "" || received.Cookie != "" || received.Origin != "" || received.Referer != "" {
				t.Fatalf("sensitive upstream headers were forwarded: %#v", received)
			}
		})
	}
}

func TestMapTileProxyRejectsInvalidRequests(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxies, err := newMapTileProxySet(testMapTileUpstreams(t, upstream.URL))
	if err != nil {
		t.Fatalf("newMapTileProxySet() error = %v", err)
	}
	s := newTestServer(t, store.New(10, 10))
	s.mapTiles = proxies
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{
			name:       "unknown parameter",
			method:     http.MethodGet,
			path:       "/amap-road-tile?x=1&y=1&z=2&target=https://example.com",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing coordinate",
			method:     http.MethodGet,
			path:       "/amap-road-tile?y=1&z=2",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "duplicate coordinate",
			method:     http.MethodGet,
			path:       "/amap-road-tile?x=1&x=2&y=1&z=2",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non numeric coordinate",
			method:     http.MethodGet,
			path:       "/amap-road-tile?x=abc&y=1&z=2",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "coordinate outside zoom",
			method:     http.MethodGet,
			path:       "/amap-road-tile?x=8&y=1&z=3",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "wrong fixed AMap style",
			method:     http.MethodGet,
			path:       "/amap-road-tile?style=6&x=1&y=1&z=2",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "AMap satellite zoom too high",
			method:     http.MethodGet,
			path:       "/amap-satellite-tile?x=1&y=1&z=17",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unsupported Google layer",
			method:     http.MethodGet,
			path:       "/google-tile?lyrs=h&x=1&y=1&z=2",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Google satellite zoom too high",
			method:     http.MethodGet,
			path:       "/google-tile?lyrs=s&x=1&y=1&z=22",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "method not allowed",
			method:     http.MethodPost,
			path:       "/google-tile?lyrs=m&x=1&y=1&z=2",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			s.server.Handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
	if calls := upstreamCalls.Load(); calls != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls)
	}
}

func TestMapTileProxyDoesNotCacheUpstreamErrors(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("Set-Cookie", "upstream=value")
		http.Error(w, "upstream failed", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	proxies, err := newMapTileProxySet(testMapTileUpstreams(t, upstream.URL))
	if err != nil {
		t.Fatalf("newMapTileProxySet() error = %v", err)
	}
	s := newTestServer(t, store.New(10, 10))
	s.mapTiles = proxies
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(
		rec,
		httptest.NewRequest(http.MethodGet, "/google-tile?lyrs=m&x=1&y=1&z=2", nil),
	)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("Cache-Control = %q, want empty", got)
	}
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("Set-Cookie = %q, want empty", got)
	}
}

func TestNewMapTileProxyRejectsUnsafeUpstreams(t *testing.T) {
	tests := []struct {
		name     string
		upstream url.URL
	}{
		{
			name: "unsupported scheme",
			upstream: url.URL{
				Scheme: "file",
				Host:   "localhost",
				Path:   "/tmp/tile",
			},
		},
		{
			name: "missing host",
			upstream: url.URL{
				Scheme: "https",
				Path:   "/tile",
			},
		},
		{
			name: "missing path",
			upstream: url.URL{
				Scheme: "https",
				Host:   "example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newMapTileProxy(tt.upstream, normalizeGoogleTileQuery, http.DefaultTransport)
			if err == nil {
				t.Fatal("newMapTileProxy() error = nil, want validation error")
			}
		})
	}
}

type mapTileUpstreamRequest struct {
	Path          string     `json:"path"`
	Query         url.Values `json:"query"`
	Host          string     `json:"host"`
	UserAgent     string     `json:"userAgent"`
	Authorization string     `json:"authorization"`
	Cookie        string     `json:"cookie"`
	Origin        string     `json:"origin"`
	Referer       string     `json:"referer"`
}

func testMapTileUpstreams(t *testing.T, rawBaseURL string) mapTileUpstreams {
	t.Helper()
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	amapRoad := *baseURL
	amapRoad.Path = "/amap-road"
	amapSatellite := *baseURL
	amapSatellite.Path = "/amap-satellite"
	google := *baseURL
	google.Path = "/google"
	return mapTileUpstreams{
		AMapRoad:      amapRoad,
		AMapSatellite: amapSatellite,
		Google:        google,
	}
}
