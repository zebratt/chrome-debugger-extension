package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPolicyOnlyAllowsRegularWebPages(t *testing.T) {
	p := Default()
	for _, url := range []string{"https://example.com/path", "http://localhost:3000/"} {
		if !p.AllowsURL(url, false) {
			t.Errorf("default policy rejected %s", url)
		}
	}
	for _, url := range []string{"chrome://extensions", "file:///tmp/example.html", "javascript:alert(1)"} {
		if p.AllowsURL(url, false) {
			t.Errorf("default policy allowed %s", url)
		}
	}
	if p.AllowsURL("https://example.com", true) {
		t.Fatal("incognito page was allowed")
	}
}

func TestBlockedSiteWinsOverAllowlist(t *testing.T) {
	p := Policy{AllowedSites: []string{"*.example.com"}, BlockedSites: []string{"private.example.com"}}
	if !p.AllowsURL("https://public.example.com", false) {
		t.Fatal("public subdomain was rejected")
	}
	if p.AllowsURL("https://private.example.com", false) {
		t.Fatal("blocked subdomain was allowed")
	}
	if p.AllowsURL("https://unrelated.com", false) {
		t.Fatal("site outside allowlist was allowed")
	}
}

func TestCDPRequiresExplicitEnableAndDomain(t *testing.T) {
	p := Default()
	if p.AllowsCDP("Network.enable") {
		t.Fatal("CDP allowed by default")
	}
	p.RawCDP = true
	p.CDPDomains = []string{"Network"}
	if !p.AllowsCDP("Network.enable") || p.AllowsCDP("Runtime.evaluate") || p.AllowsCDP("invalid") {
		t.Fatal("CDP domain policy mismatch")
	}
}

func TestLoadPolicyUsesDefaultsAndRejectsBroadFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	missing, err := Load(path)
	if err != nil || missing.RawCDP {
		t.Fatalf("missing policy %+v, %v", missing, err)
	}
	if err := os.WriteFile(path, []byte(`{"blockedSites":["secret.example.com"],"rawCdp":true,"cdpDomains":["Network"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("world-readable config was accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.AllowsURL("https://secret.example.com", false) || !loaded.AllowsCDP("Network.enable") {
		t.Fatalf("loaded policy %+v, %v", loaded, err)
	}
}
