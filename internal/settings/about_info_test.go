package settings

import (
	"os"
	"path/filepath"
	"testing"

	"drone-management/internal/model"
)

func TestLoadAboutInfo(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		create  bool
		want    model.AboutInfo
		wantErr bool
	}{
		{name: "missing file leaves fields empty"},
		{name: "empty object", create: true, data: `{}`},
		{name: "trim names and ignore manufacturer", create: true, data: `{"userCompany":"  使用厂家  ","userName":"  使用人员  ","manufacturer":"自定义厂家"}`, want: model.AboutInfo{UserCompany: "使用厂家", UserName: "使用人员"}},
		{name: "invalid JSON", create: true, data: `{`, wantErr: true},
		{name: "invalid field type", create: true, data: `{"userName":123}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "about.json")
			if tt.create {
				if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := LoadAboutInfo(path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadAboutInfo() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("LoadAboutInfo() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
