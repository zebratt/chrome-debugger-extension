package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	Root         string
	HostManifest string
	SourceBinary string
	ExtensionDir string
	ExtensionID  string
}

type Result struct {
	ExtensionDir string
	BinaryPath   string
	ManifestPath string
	ExtensionID  string
}

var extensionIDPattern = regexp.MustCompile(`^[a-p]{32}$`)

func Install(config Config) (Result, error) {
	if !extensionIDPattern.MatchString(config.ExtensionID) {
		return Result{}, errors.New("extension ID must be 32 characters from a to p")
	}
	if !filepath.IsAbs(config.Root) || !filepath.IsAbs(config.HostManifest) {
		return Result{}, errors.New("installation paths must be absolute")
	}
	if info, err := os.Stat(filepath.Join(config.ExtensionDir, "manifest.json")); err != nil || !info.Mode().IsRegular() {
		return Result{}, errors.New("extension directory has no manifest.json")
	}
	info, err := os.Stat(config.SourceBinary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return Result{}, errors.New("source binary is missing or not executable")
	}
	contents, err := os.ReadFile(config.SourceBinary)
	if err != nil {
		return Result{}, err
	}
	digest := sha256.Sum256(contents)
	release := hex.EncodeToString(digest[:])[:12]
	root := config.Root
	releaseDir := filepath.Join(root, "releases", release)
	if err := os.MkdirAll(releaseDir, 0755); err != nil {
		return Result{}, err
	}
	installedBinary := filepath.Join(releaseDir, "chrome-connector")
	installed, err := os.ReadFile(installedBinary)
	if os.IsNotExist(err) || (err == nil && sha256.Sum256(installed) != digest) {
		if err := writeAtomic(installedBinary, contents, 0755); err != nil {
			return Result{}, err
		}
	} else if err != nil {
		return Result{}, err
	}
	current := filepath.Join(root, "current")
	newTarget := filepath.Join("releases", release, "chrome-connector")
	if previous, err := os.Readlink(current); err == nil && previous != newTarget {
		if err := replaceSymlink(filepath.Join(root, "previous"), previous); err != nil {
			return Result{}, err
		}
	}
	if err := replaceSymlink(current, newTarget); err != nil {
		return Result{}, err
	}
	policyPath := filepath.Join(root, "config.json")
	if _, err := os.Stat(policyPath); os.IsNotExist(err) {
		if err := writeAtomic(policyPath, []byte("{\n  \"blockedSites\": [],\n  \"rawCdp\": false,\n  \"cdpDomains\": []\n}\n"), 0600); err != nil {
			return Result{}, err
		}
	} else if err != nil {
		return Result{}, err
	}
	manifest := map[string]any{
		"name":            "com.chromeconnector.bridge",
		"description":     "Chrome Connector local agent bridge",
		"path":            current,
		"type":            "stdio",
		"allowed_origins": []string{"chrome-extension://" + config.ExtensionID + "/"},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(config.HostManifest), 0700); err != nil {
		return Result{}, err
	}
	if err := writeAtomic(config.HostManifest, append(encoded, '\n'), 0600); err != nil {
		return Result{}, err
	}
	return Result{ExtensionDir: config.ExtensionDir, BinaryPath: current, ManifestPath: config.HostManifest, ExtensionID: config.ExtensionID}, nil
}

func Rollback(root string) error {
	current := filepath.Join(root, "current")
	previous := filepath.Join(root, "previous")
	currentTarget, err := os.Readlink(current)
	if err != nil {
		return fmt.Errorf("current version is unavailable: %w", err)
	}
	previousTarget, err := os.Readlink(previous)
	if err != nil {
		return fmt.Errorf("previous version is unavailable: %w", err)
	}
	for _, target := range []string{currentTarget, previousTarget} {
		resolved := filepath.Clean(filepath.Join(root, target))
		if !strings.HasPrefix(resolved, filepath.Clean(root)+string(os.PathSeparator)) {
			return errors.New("release target escapes installation root")
		}
		if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
			return errors.New("release target is unavailable")
		}
	}
	if err := replaceSymlink(current, previousTarget); err != nil {
		return err
	}
	if err := replaceSymlink(previous, currentTarget); err != nil {
		replaceSymlink(current, currentTarget)
		return err
	}
	return nil
}

func replaceSymlink(path, target string) error {
	temporary := path + ".next"
	if err := os.Remove(temporary); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace symlink %s: %w", path, err)
	}
	return nil
}

func writeAtomic(path string, contents []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".chrome-connector-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
