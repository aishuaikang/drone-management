package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"drone-management/internal/model"
	"drone-management/internal/store"
)

func TestAboutInfoRouteAvailableWithoutLicenseAndReloads(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	s.license = nil
	s.cfg.AboutInfoPath = filepath.Join(t.TempDir(), "about.json")
	for _, want := range []model.AboutInfo{{}, {UserCompany: "使用厂家", UserName: "使用人员"}, {UserCompany: "另一厂家"}} {
		if want != (model.AboutInfo{}) {
			data, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(s.cfg.AboutInfoPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		rec := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/about", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var got model.AboutInfo
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("about information = %#v, want %#v", got, want)
		}
	}
}

func TestAboutInfoCannotBeEditedByPublicAPI(t *testing.T) {
	s := newTestServer(t, store.New(10, 10))
	s.userSettings = &memoryUserSettingsStore{}
	s.cfg.AboutInfoPath = filepath.Join(t.TempDir(), "about.json")
	original := `{"userCompany":"原使用厂家","userName":"原使用人员"}`
	if err := os.WriteFile(s.cfg.AboutInfoPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/about", strings.NewReader(`{"userCompany":"修改后的厂家"}`)))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /about status = %d, want 404 or 405", method, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/user/settings", strings.NewReader(`{"userCompany":"修改后的厂家","userName":"修改后的人员"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("update settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(s.cfg.AboutInfoPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("public settings changed software information: %s", data)
	}
}
