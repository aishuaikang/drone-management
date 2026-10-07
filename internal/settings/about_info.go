package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"drone-management/internal/model"
)

// LoadAboutInfo reads software information managed outside the public settings API.
func LoadAboutInfo(path string) (model.AboutInfo, error) {
	if strings.TrimSpace(path) == "" {
		return model.AboutInfo{}, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return model.AboutInfo{}, nil
	}
	if err != nil {
		return model.AboutInfo{}, err
	}
	var info model.AboutInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return model.AboutInfo{}, fmt.Errorf("decode about information: %w", err)
	}
	info.UserCompany = strings.TrimSpace(info.UserCompany)
	info.UserName = strings.TrimSpace(info.UserName)
	return info, nil
}
