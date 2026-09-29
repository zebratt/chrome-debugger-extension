package policy

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
)

func Load(path string) (Policy, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Policy{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return Policy{}, errors.New("policy file must be a private regular file")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, err
	}
	var active Policy
	if err := json.Unmarshal(contents, &active); err != nil {
		return Policy{}, err
	}
	return active, nil
}

type Policy struct {
	AllowedSites []string `json:"allowedSites"`
	BlockedSites []string `json:"blockedSites"`
	RawCDP       bool     `json:"rawCdp"`
	CDPDomains   []string `json:"cdpDomains"`
}

func Default() Policy { return Policy{} }

func (policy Policy) AllowsURL(rawURL string, incognito bool) bool {
	if incognito {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, pattern := range policy.BlockedSites {
		if matchHost(pattern, host) {
			return false
		}
	}
	if len(policy.AllowedSites) == 0 {
		return true
	}
	for _, pattern := range policy.AllowedSites {
		if matchHost(pattern, host) {
			return true
		}
	}
	return false
}

func (policy Policy) AllowsCDP(method string) bool {
	if !policy.RawCDP {
		return false
	}
	domain, command, ok := strings.Cut(method, ".")
	if !ok || domain == "" || command == "" {
		return false
	}
	for _, allowed := range policy.CDPDomains {
		if domain == allowed {
			return true
		}
	}
	return false
}

func matchHost(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(host, pattern[1:]) && host != pattern[2:]
	}
	return pattern == host
}
