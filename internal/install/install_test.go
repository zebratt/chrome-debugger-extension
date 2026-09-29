package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallRegistersExactExtensionOriginAndPrivatePolicy(t *testing.T) {
	base := t.TempDir()
	binary := filepath.Join(base, "source-binary")
	if err := os.WriteFile(binary, []byte("binary-one"), 0755); err != nil {
		t.Fatal(err)
	}
	extension := filepath.Join(base, "extension")
	if err := os.Mkdir(extension, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extension, "manifest.json"), []byte(`{"manifest_version":3}`), 0644); err != nil {
		t.Fatal(err)
	}
	config := Config{
		Root:         filepath.Join(base, "installed"),
		HostManifest: filepath.Join(base, "chrome", "com.chromeconnector.bridge.json"),
		SourceBinary: binary,
		ExtensionDir: extension,
		ExtensionID:  "lmomiiblpebceaecbnlknkbnanhhdigi",
	}
	result, err := Install(config)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExtensionDir != extension {
		t.Fatalf("extension path %q", result.ExtensionDir)
	}
	contents, err := os.ReadFile(filepath.Join(config.Root, "current"))
	if err != nil || string(contents) != "binary-one" {
		t.Fatalf("installed binary %q, %v", contents, err)
	}
	var manifest struct {
		Path           string   `json:"path"`
		AllowedOrigins []string `json:"allowed_origins"`
	}
	bytes, err := os.ReadFile(config.HostManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Path != filepath.Join(config.Root, "current") || len(manifest.AllowedOrigins) != 1 || manifest.AllowedOrigins[0] != "chrome-extension://lmomiiblpebceaecbnlknkbnanhhdigi/" {
		t.Fatalf("manifest %+v", manifest)
	}
	policyInfo, err := os.Stat(filepath.Join(config.Root, "config.json"))
	if err != nil || policyInfo.Mode().Perm() != 0600 {
		t.Fatalf("policy permissions %v, %v", policyInfo, err)
	}
}

func TestInstallRejectsInvalidExtensionIDBeforeWritingManifest(t *testing.T) {
	base := t.TempDir()
	config := Config{Root: filepath.Join(base, "installed"), HostManifest: filepath.Join(base, "host.json"), ExtensionID: "../../bad"}
	if _, err := Install(config); err == nil {
		t.Fatal("invalid extension ID accepted")
	}
	if _, err := os.Stat(config.HostManifest); !os.IsNotExist(err) {
		t.Fatalf("manifest should be absent: %v", err)
	}
}

func TestRollbackSwitchesToPreviousBinary(t *testing.T) {
	base := t.TempDir()
	extension := filepath.Join(base, "extension")
	if err := os.Mkdir(extension, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extension, "manifest.json"), []byte(`{"manifest_version":3}`), 0644); err != nil {
		t.Fatal(err)
	}
	config := Config{Root: filepath.Join(base, "installed"), HostManifest: filepath.Join(base, "host.json"), ExtensionDir: extension, ExtensionID: "lmomiiblpebceaecbnlknkbnanhhdigi"}
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	os.WriteFile(first, []byte("first"), 0755)
	os.WriteFile(second, []byte("second"), 0755)
	config.SourceBinary = first
	if _, err := Install(config); err != nil {
		t.Fatal(err)
	}
	config.SourceBinary = second
	if _, err := Install(config); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(config.Root); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(filepath.Join(config.Root, "current"))
	if err != nil || string(current) != "first" {
		t.Fatalf("current %q, %v", current, err)
	}
	previous, err := os.ReadFile(filepath.Join(config.Root, "previous"))
	if err != nil || string(previous) != "second" {
		t.Fatalf("previous %q, %v", previous, err)
	}
}

func TestInstallRepairsIncompleteReleaseFile(t *testing.T) {
	base := t.TempDir()
	extension := filepath.Join(base, "extension")
	os.Mkdir(extension, 0755)
	os.WriteFile(filepath.Join(extension, "manifest.json"), []byte(`{"manifest_version":3}`), 0644)
	binary := filepath.Join(base, "source")
	os.WriteFile(binary, []byte("complete-binary"), 0755)
	config := Config{Root: filepath.Join(base, "installed"), HostManifest: filepath.Join(base, "host.json"), SourceBinary: binary, ExtensionDir: extension, ExtensionID: "lmomiiblpebceaecbnlknkbnanhhdigi"}
	if _, err := Install(config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Root, "current"), []byte("partial"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(config.Root, "current"))
	if err != nil || string(data) != "complete-binary" {
		t.Fatalf("release was not repaired %q, %v", data, err)
	}
}
