// Package offlinemap validates and installs offline map tile packages.
package offlinemap

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dr600ab-net/internal/model"
)

const manifestName = ".offline-map.json"

// Service installs normalized map tiles under a static map directory.
type Service struct {
	root string
}

// UploadOptions controls offline map package installation.
type UploadOptions struct {
	SourceFile string
	KeepBackup bool
	Now        time.Time
}

type manifest struct {
	TileCount  int       `json:"tileCount"`
	UploadedAt time.Time `json:"uploadedAt"`
	SourceFile string    `json:"sourceFile,omitempty"`
}

type tileEntry struct {
	source *zip.File
	target string
}

type offlineMapLayout struct {
	StripPrefix string
}

// NewService creates an offline map service rooted at path.
func NewService(root string) *Service {
	return &Service{root: strings.TrimSpace(root)}
}

// Status returns the current installed offline map state.
func (s *Service) Status() model.OfflineMapStatus {
	if s == nil || s.root == "" {
		return model.OfflineMapStatus{
			Available: false,
			Message:   "offline map path is not configured",
		}
	}
	status := model.OfflineMapStatus{
		Path: s.root,
	}
	data, err := os.ReadFile(filepath.Join(s.root, manifestName))
	if err == nil {
		var saved manifest
		if json.Unmarshal(data, &saved) == nil {
			uploadedAt := saved.UploadedAt
			status.TileCount = saved.TileCount
			status.UploadedAt = &uploadedAt
			status.SourceFile = saved.SourceFile
		}
	}
	if _, err := os.Stat(filepath.Join(s.root, "dt")); err == nil {
		status.Available = true
		if status.TileCount == 0 {
			status.TileCount = countTiles(filepath.Join(s.root, "dt"))
		}
		return status
	}
	if err != nil && !os.IsNotExist(err) {
		status.Message = err.Error()
	}
	return status
}

// Install validates a zip package and atomically switches the active map directory.
func (s *Service) Install(packagePath string, options UploadOptions) (model.OfflineMapStatus, error) {
	if s == nil || s.root == "" {
		return model.OfflineMapStatus{}, fmt.Errorf("offline map path is not configured")
	}
	packagePath = strings.TrimSpace(packagePath)
	if packagePath == "" {
		return model.OfflineMapStatus{}, fmt.Errorf("请选择离线地图 ZIP 包")
	}
	if !strings.EqualFold(filepath.Ext(packagePath), ".zip") {
		return model.OfflineMapStatus{}, fmt.Errorf("离线地图只支持 .zip 文件")
	}

	entries, cleanup, err := collectTiles(packagePath)
	if err != nil {
		return model.OfflineMapStatus{}, err
	}
	defer cleanup()

	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	sourceFile := strings.TrimSpace(options.SourceFile)
	if sourceFile == "" {
		sourceFile = filepath.Base(packagePath)
	}
	if err := s.installEntries(entries, manifest{
		TileCount:  len(entries),
		UploadedAt: now,
		SourceFile: sourceFile,
	}, options.KeepBackup); err != nil {
		return model.OfflineMapStatus{}, err
	}
	return s.Status(), nil
}

func (s *Service) installEntries(entries []tileEntry, saved manifest, keepBackup bool) error {
	taskID := strconv.FormatInt(time.Now().UnixNano(), 10)
	stagingDir := filepath.Join(s.root, ".staging", taskID)
	stagingDT := filepath.Join(stagingDir, "dt")
	currentDT := filepath.Join(s.root, "dt")
	backupRoot := filepath.Join(s.root, ".backup")
	backupDT := filepath.Join(backupRoot, "dt_"+time.Now().Format("20060102150405"))

	if err := os.MkdirAll(stagingDT, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		_ = os.RemoveAll(stagingDir)
		return err
	}
	cleanupStaging := true
	defer func() {
		if cleanupStaging {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	for _, entry := range entries {
		targetPath := filepath.Join(stagingDir, filepath.FromSlash(entry.target))
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		if err := writeTile(entry.source, targetPath); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}

	backupCreated := false
	if _, err := os.Stat(currentDT); err == nil {
		if err := os.Rename(currentDT, backupDT); err != nil {
			return err
		}
		backupCreated = true
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(stagingDT, currentDT); err != nil {
		if backupCreated {
			_ = os.Rename(backupDT, currentDT)
		}
		return err
	}
	cleanupStaging = false
	_ = os.RemoveAll(stagingDir)

	if err := writeManifest(s.root, saved); err != nil {
		if backupCreated {
			_ = os.RemoveAll(currentDT)
			_ = os.Rename(backupDT, currentDT)
		}
		return err
	}
	if !keepBackup && backupCreated {
		_ = os.RemoveAll(backupDT)
	}
	return syncDir(s.root)
}

func writeTile(file *zip.File, targetPath string) error {
	source, err := file.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer target.Close()
	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return target.Sync()
}

func writeManifest(root string, saved manifest) error {
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, manifestName)
	temp, err := os.CreateTemp(root, ".offline-map-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if _, err := temp.Write([]byte("\n")); err != nil {
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func collectTiles(sourcePath string) ([]tileEntry, func(), error) {
	reader, err := zip.OpenReader(sourcePath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("打开 ZIP 失败: %w", err)
	}
	cleanup := func() { _ = reader.Close() }

	layout, err := detectPackageLayout(&reader.Reader)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}

	seen := map[string]struct{}{}
	entries := make([]tileEntry, 0)
	for _, file := range reader.File {
		name := normalizeZipEntryName(file.Name)
		if name == "" || isIgnorableZipEntry(name) || file.FileInfo().IsDir() || isXML(name) {
			continue
		}
		if err := validateOfflineMapEntry(file, name); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		rel, ok := layout.normalize(name)
		if !ok {
			cleanup()
			return nil, func() {}, fmt.Errorf("离线地图包存在混合目录结构: %s", name)
		}
		if _, exists := seen[rel]; exists {
			continue
		}
		seen[rel] = struct{}{}
		entries = append(entries, tileEntry{source: file, target: rel})
	}
	if len(entries) == 0 {
		cleanup()
		return nil, func() {}, fmt.Errorf("离线地图包中没有可提取瓦片")
	}
	return entries, cleanup, nil
}

func detectPackageLayout(reader *zip.Reader) (offlineMapLayout, error) {
	var layout offlineMapLayout
	layoutSet := false
	for _, file := range reader.File {
		name := normalizeZipEntryName(file.Name)
		if name == "" || isIgnorableZipEntry(name) || file.FileInfo().IsDir() {
			continue
		}
		if err := validateOfflineMapEntry(file, name); err != nil {
			return offlineMapLayout{}, err
		}
		if isXML(name) {
			continue
		}
		detected, ok := detectOfflineMapTileLayout(name)
		if !ok {
			return offlineMapLayout{}, fmt.Errorf("不支持的离线地图瓦片路径: %s", name)
		}
		if !layoutSet {
			layout = detected
			layoutSet = true
		}
	}
	if !layoutSet {
		return offlineMapLayout{}, fmt.Errorf("离线地图包中没有有效瓦片")
	}
	return layout, nil
}

func validateOfflineMapEntry(file *zip.File, name string) error {
	raw := strings.TrimSpace(file.Name)
	rawSlash := filepath.ToSlash(raw)
	invalidPath := raw == "" ||
		strings.HasPrefix(raw, "/") ||
		strings.HasPrefix(raw, "\\") ||
		filepath.IsAbs(raw) ||
		strings.Contains(raw, ":") ||
		hasParentPathSegment(rawSlash)
	if invalidPath || strings.Contains(name, "..") {
		return fmt.Errorf("ZIP 包含非法路径: %s", file.Name)
	}
	if file.FileInfo().Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("ZIP 包含符号链接: %s", name)
	}
	if hasHiddenZipPart(name) {
		return fmt.Errorf("ZIP 包含隐藏或系统目录: %s", name)
	}
	lower := strings.ToLower(name)
	for _, ext := range []string{".exe", ".dll", ".so", ".dylib", ".sh", ".bat", ".cmd", ".ps1", ".php", ".jsp", ".asp", ".aspx", ".sql", ".db", ".sqlite"} {
		if strings.HasSuffix(lower, ext) {
			return fmt.Errorf("ZIP 包含不允许的文件类型: %s", name)
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".xml" || ext == ".jpg" || ext == ".jpeg" {
		return nil
	}
	return fmt.Errorf("离线地图只支持 JPG/JPEG 瓦片: %s", name)
}

func detectOfflineMapTileLayout(name string) (offlineMapLayout, bool) {
	parts := strings.Split(name, "/")
	if len(parts) >= 4 && parts[0] == "dt" && isValidTileXYZ(parts[1], parts[2], parts[3]) {
		return offlineMapLayout{StripPrefix: "dt"}, true
	}
	if len(parts) >= 5 && parts[1] == "dt" && isValidTileXYZ(parts[2], parts[3], parts[4]) {
		return offlineMapLayout{StripPrefix: parts[0] + "/dt"}, true
	}
	if len(parts) >= 3 && isValidTileXYZ(parts[0], parts[1], parts[2]) {
		return offlineMapLayout{}, true
	}
	if len(parts) >= 4 && isValidTileXYZ(parts[1], parts[2], parts[3]) {
		return offlineMapLayout{StripPrefix: parts[0]}, true
	}
	return offlineMapLayout{}, false
}

func (l offlineMapLayout) normalize(name string) (string, bool) {
	trimmed := name
	if l.StripPrefix != "" {
		prefix := strings.Trim(l.StripPrefix, "/") + "/"
		if !strings.HasPrefix(name, prefix) {
			return "", false
		}
		trimmed = strings.TrimPrefix(name, prefix)
	}
	if trimmed == "" || isXML(trimmed) {
		return "", false
	}
	return normalizeTileExtension("dt/" + trimmed), true
}

func isValidTileXYZ(zText, xText, yFile string) bool {
	z, err := strconv.Atoi(zText)
	if err != nil || z < 0 || z > 22 {
		return false
	}
	if _, err := strconv.Atoi(xText); err != nil {
		return false
	}
	yText := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(yFile), ".jpeg"), ".jpg")
	if _, err := strconv.Atoi(yText); err != nil {
		return false
	}
	return true
}

func normalizeZipEntryName(name string) string {
	name = filepath.ToSlash(strings.TrimSpace(name))
	name = strings.TrimLeft(name, "/")
	cleaned := filepath.ToSlash(filepath.Clean(name))
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func normalizeTileExtension(name string) string {
	ext := filepath.Ext(name)
	if strings.EqualFold(ext, ".jpeg") {
		return strings.TrimSuffix(name, ext) + ".jpg"
	}
	return name
}

func isXML(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".xml")
}

func isIgnorableZipEntry(name string) bool {
	if name == "" {
		return true
	}
	for _, part := range strings.Split(name, "/") {
		if part == "__MACOSX" || part == ".DS_Store" || strings.HasPrefix(part, "._") {
			return true
		}
	}
	return false
}

func hasHiddenZipPart(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, ".") || part == "__MACOSX" {
			return true
		}
	}
	return false
}

func hasParentPathSegment(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func countTiles(root string) int {
	count := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".jpg") || strings.EqualFold(filepath.Ext(path), ".jpeg") {
			count++
		}
		return nil
	})
	return count
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
