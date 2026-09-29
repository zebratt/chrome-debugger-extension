package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckReportsRegisteredHostAndPrivatePolicy(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "chrome-connector")
	if err := os.WriteFile(binary, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(directory, "host.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"com.chromeconnector.bridge","path":"`+binary+`","allowed_origins":["chrome-extension://lmomiiblpebceaecbnlknkbnanhhdigi/"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(directory, "config.json")
	if err := os.WriteFile(policy, []byte(`{"rawCdp":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	report := Check(manifest, policy)
	if !report.HostManifest || !report.HostBinary || !report.PolicyValid || report.ExtensionID != "lmomiiblpebceaecbnlknkbnanhhdigi" || len(report.Issues) != 0 {
		t.Fatalf("report %+v", report)
	}
}

func TestCheckExplainsMissingRegistration(t *testing.T) {
	directory := t.TempDir()
	report := Check(filepath.Join(directory, "host.json"), filepath.Join(directory, "config.json"))
	if report.HostManifest || len(report.Issues) == 0 {
		t.Fatalf("report %+v", report)
	}
}
