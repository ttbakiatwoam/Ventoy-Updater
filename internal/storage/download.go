package storage

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ventoy-update/internal/logging"
	"ventoy-update/internal/providers"
)

const freeSpaceMargin = 512 << 20

type DownloadOptions struct {
	DryRun   bool
	KeepOld  bool
	NoDelete bool
}

func DownloadRelease(ctx context.Context, client *providers.HTTPClient, target string, release providers.Release, installedFilename string, opts DownloadOptions, logger *logging.Logger) error {
	if release.Filename == "" || release.URL == "" {
		return fmt.Errorf("%s has incomplete release metadata", release.ImageID)
	}
	if release.Checksum == "" || release.ChecksumType == "" {
		return fmt.Errorf("%s has no checksum; refusing unsafe download", release.ImageID)
	}

	dest := filepath.Join(target, release.Filename)
	partPath := filepath.Join(target, ToolDirName, "downloads", release.Filename+".part")
	if opts.DryRun {
		if logger != nil {
			logger.Info("dry run download", logging.Fields{
				"image":    release.ImageID,
				"filename": release.Filename,
				"url":      release.URL,
			})
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(partPath), 0o755); err != nil {
		return err
	}
	if err := ensureSpace(target, partPath, release.Size); err != nil {
		return err
	}

	if same, err := fileChecksumMatches(dest, release.ChecksumType, release.Checksum); err == nil && same {
		if logger != nil {
			logger.Info("already verified", logging.Fields{"image": release.ImageID, "filename": release.Filename})
		}
		return maybeDeleteOld(target, installedFilename, release.Filename, opts, logger)
	}
	if same, err := fileChecksumMatches(partPath, release.ChecksumType, release.Checksum); err == nil && same {
		if logger != nil {
			logger.Info("using verified staged download", logging.Fields{"image": release.ImageID, "filename": release.Filename})
		}
		return installVerifiedPart(target, partPath, dest, installedFilename, release.Filename, opts, logger)
	}

	if err := downloadPart(ctx, client, release, partPath, logger); err != nil {
		return err
	}
	if same, err := fileChecksumMatches(partPath, release.ChecksumType, release.Checksum); err != nil {
		return err
	} else if !same {
		return fmt.Errorf("checksum mismatch for %s", release.Filename)
	}
	return installVerifiedPart(target, partPath, dest, installedFilename, release.Filename, opts, logger)
}

func ensureSpace(target, partPath string, expectedSize int64) error {
	if expectedSize <= 0 {
		return nil
	}
	have := FileSize(partPath)
	needed := expectedSize - have + freeSpaceMargin
	if needed < freeSpaceMargin {
		needed = freeSpaceMargin
	}
	available, err := AvailableBytes(target)
	if err != nil {
		return err
	}
	if available < needed {
		return fmt.Errorf("not enough free space on target: need at least %d bytes, available %d bytes", needed, available)
	}
	return nil
}

func downloadPart(ctx context.Context, client *providers.HTTPClient, release providers.Release, partPath string, logger *logging.Logger) error {
	start := FileSize(partPath)
	if release.Size > 0 && start >= release.Size {
		if err := os.Remove(partPath); err != nil {
			return err
		}
		start = 0
	}

	headers := map[string]string{}
	if start > 0 {
		headers["Range"] = fmt.Sprintf("bytes=%d-", start)
	}
	resp, err := client.Do(ctx, http.MethodGet, release.URL, headers, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if release.Size <= 0 {
		if resp.ContentLength > 0 {
			if err := ensureAdditionalSpace(filepath.Dir(partPath), resp.ContentLength); err != nil {
				return err
			}
		} else if logger != nil {
			logger.Warn("download size unknown; free-space check is best effort", logging.Fields{"image": release.ImageID})
		}
	}

	flags := os.O_CREATE | os.O_WRONLY
	if start > 0 && resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		if start > 0 && logger != nil {
			logger.Warn("server did not resume; restarting download", logging.Fields{"image": release.ImageID})
		}
		start = 0
		flags |= os.O_TRUNC
	}

	file, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	if logger != nil {
		logger.Info("download", logging.Fields{
			"image":  release.ImageID,
			"file":   release.Filename,
			"resume": start,
		})
	}
	if _, err := io.CopyBuffer(file, resp.Body, make([]byte, 1024*1024)); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if release.Size > 0 {
		size := FileSize(partPath)
		if size != release.Size {
			return fmt.Errorf("downloaded size for %s is %d bytes, expected %d bytes", release.Filename, size, release.Size)
		}
	}
	return nil
}

func ensureAdditionalSpace(target string, bytesNeeded int64) error {
	available, err := AvailableBytes(target)
	if err != nil {
		return err
	}
	needed := bytesNeeded + freeSpaceMargin
	if available < needed {
		return fmt.Errorf("not enough free space on target: need at least %d bytes, available %d bytes", needed, available)
	}
	return nil
}

func installVerifiedPart(target, partPath, dest, installedFilename, releaseFilename string, opts DownloadOptions, logger *logging.Logger) error {
	backupPath := ""
	if _, err := os.Stat(dest); err == nil {
		backupDir := filepath.Join(target, ToolDirName, "backups")
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return err
		}
		backupPath = filepath.Join(backupDir, filepath.Base(dest)+"."+time.Now().UTC().Format("20060102T150405")+"Z.old")
		if logger != nil {
			logger.Info("backup existing file", logging.Fields{"from": dest, "to": backupPath})
		}
		if err := os.Rename(dest, backupPath); err != nil {
			return err
		}
	}

	if logger != nil {
		logger.Info("install verified image", logging.Fields{"from": partPath, "to": dest})
	}
	if err := os.Rename(partPath, dest); err != nil {
		if backupPath != "" {
			_ = os.Rename(backupPath, dest)
		}
		return err
	}
	if backupPath != "" && !opts.KeepOld && !opts.NoDelete {
		if logger != nil {
			logger.Info("remove replacement backup", logging.Fields{"path": backupPath})
		}
		if err := os.Remove(backupPath); err != nil {
			return err
		}
	}
	return maybeDeleteOld(target, installedFilename, releaseFilename, opts, logger)
}

func maybeDeleteOld(target, installedFilename, releaseFilename string, opts DownloadOptions, logger *logging.Logger) error {
	if installedFilename == "" || installedFilename == releaseFilename || opts.KeepOld || opts.NoDelete {
		return nil
	}
	oldPath := filepath.Join(target, installedFilename)
	if _, err := os.Stat(oldPath); err != nil {
		return nil
	}
	if logger != nil {
		logger.Info("remove old image", logging.Fields{"path": oldPath})
	}
	return os.Remove(oldPath)
}

func fileChecksumMatches(path, checksumType, expected string) (bool, error) {
	if expected == "" {
		return false, nil
	}
	actual, err := HashFile(path, checksumType)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(actual, expected), nil
}

func HashFile(path, checksumType string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher, err := newHasher(checksumType)
	if err != nil {
		return "", err
	}
	if _, err := io.CopyBuffer(hasher, file, make([]byte, 1024*1024)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func newHasher(checksumType string) (hash.Hash, error) {
	switch strings.ToLower(checksumType) {
	case "md5":
		return md5.New(), nil
	case "sha1":
		return sha1.New(), nil
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported checksum type %q", checksumType)
	}
}
