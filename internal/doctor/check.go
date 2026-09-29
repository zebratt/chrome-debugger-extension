package doctor

import (
	"encoding/json"
	"os"
	"strings"

	"chrome-connector/internal/policy"
)

type Report struct {
	HostManifest          bool              `json:"hostManifest"`
	HostManifestPath      string            `json:"hostManifestPath"`
	HostBinary            bool              `json:"hostBinary"`
	HostBinaryPath        string            `json:"hostBinaryPath,omitempty"`
	PolicyValid           bool              `json:"policyValid"`
	ExtensionID           string            `json:"extensionId,omitempty"`
	BrowserState          string            `json:"browserState,omitempty"`
	ConnectedProfiles     int               `json:"connectedProfiles"`
	BrokerProtocolVersion string            `json:"brokerProtocolVersion,omitempty"`
	BrokerVersion         string            `json:"brokerVersion,omitempty"`
	ExtensionVersions     map[string]string `json:"extensionVersions,omitempty"`
	HostVersions          map[string]string `json:"hostVersions,omitempty"`
	Issues                []string          `json:"issues"`
}

func Check(manifestPath, policyPath string) Report {
	report := Report{HostManifestPath: manifestPath, Issues: make([]string, 0)}
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		report.Issues = append(report.Issues, "Native Messaging host manifest is missing")
	} else {
		var manifest struct {
			Name           string   `json:"name"`
			Path           string   `json:"path"`
			AllowedOrigins []string `json:"allowed_origins"`
		}
		if json.Unmarshal(contents, &manifest) != nil || manifest.Name != "com.chromeconnector.bridge" || len(manifest.AllowedOrigins) != 1 {
			report.Issues = append(report.Issues, "Native Messaging host manifest is invalid")
		} else {
			report.HostManifest = true
			report.HostBinaryPath = manifest.Path
			origin := manifest.AllowedOrigins[0]
			if strings.HasPrefix(origin, "chrome-extension://") && strings.HasSuffix(origin, "/") {
				report.ExtensionID = strings.TrimSuffix(strings.TrimPrefix(origin, "chrome-extension://"), "/")
			} else {
				report.Issues = append(report.Issues, "extension origin is invalid")
			}
			if info, err := os.Stat(manifest.Path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				report.HostBinary = true
			} else {
				report.Issues = append(report.Issues, "Native Messaging host executable is unavailable")
			}
		}
	}
	if _, err := os.Stat(policyPath); err != nil {
		report.Issues = append(report.Issues, "private policy file is missing")
	} else if _, err := policy.Load(policyPath); err != nil {
		report.Issues = append(report.Issues, "private policy file is invalid: "+err.Error())
	} else {
		report.PolicyValid = true
	}
	return report
}
