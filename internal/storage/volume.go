package storage

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"ventoy-update/internal/config"
	"ventoy-update/internal/logging"
)

const ToolDirName = ".ventoy-update"

func ResolveTarget(target string) (string, error) {
	if target != "" {
		return filepath.Abs(target)
	}
	if runtime.GOOS == "darwin" {
		candidate := "/Volumes/Ventoy"
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("target not provided and /Volumes/Ventoy was not found")
}

func DefaultManifestPath(target string) string {
	return filepath.Join(target, config.DefaultManifestName)
}

func ValidateVentoyTarget(target string) error {
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", target)
	}

	base := strings.ToLower(filepath.Base(filepath.Clean(target)))
	if base == "ventoy" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(target, config.DefaultManifestName)); err == nil {
		return nil
	}
	if info, err := os.Stat(filepath.Join(target, "ventoy")); err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("%s does not look like a Ventoy image volume; expected a volume named Ventoy, an existing %s, or a ventoy directory", target, config.DefaultManifestName)
}

func EnsureToolDir(target string) (string, error) {
	path := filepath.Join(target, ToolDirName)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

func ListImages(target string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ToolDirName || isMacMetadataName(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if isMacMetadataName(name) {
			return nil
		}
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".iso") || strings.HasSuffix(lower, ".img") {
			files = append(files, name)
		}
		return nil
	})
	return files, err
}

func Cleanup(target string, removeStaging bool, logger *logging.Logger) error {
	return filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if path == target {
			return nil
		}
		if d.IsDir() && name == ToolDirName {
			if !removeStaging {
				return filepath.SkipDir
			}
			return nil
		}
		if isMacMetadataName(name) {
			if logger != nil {
				logger.Info("remove metadata", logging.Fields{"path": path})
			}
			if d.IsDir() {
				if err := os.RemoveAll(path); err != nil {
					return err
				}
				return filepath.SkipDir
			}
			return os.Remove(path)
		}
		if removeStaging && strings.HasSuffix(name, ".tmp") {
			if logger != nil {
				logger.Info("remove stale staging file", logging.Fields{"path": path})
			}
			return os.Remove(path)
		}
		return nil
	})
}

func isMacMetadataName(name string) bool {
	if strings.HasPrefix(name, "._") {
		return true
	}
	switch name {
	case ".DS_Store", ".Spotlight-V100", ".Trashes", ".fseventsd":
		return true
	default:
		return false
	}
}

func AvailableBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

func FileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
