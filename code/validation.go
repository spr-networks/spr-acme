package main

import (
	"fmt"
	"net"
	"net/mail"
	"regexp"
	"sort"
	"strings"
)

var (
	profileIDRE  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)
	providerRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	credentialRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	dnsLabelRE   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

var blockedCredentialPrefixes = []string{"LEGO_", "LD_", "DYLD_"}
var blockedCredentialNames = map[string]bool{
	"PATH": true, "HOME": true, "SHELL": true, "ENV": true, "BASH_ENV": true,
	"GODEBUG": true, "GOTRACEBACK": true, "SSLKEYLOGFILE": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
}

func validateAccountFields(email, provider, ca string) error {
	if email == "" {
		return fmt.Errorf("email is required")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return fmt.Errorf("invalid account email")
	}
	if !providerRE.MatchString(provider) || provider == "manual" || provider == "exec" {
		return fmt.Errorf("invalid or unsupported DNS provider code")
	}
	if ca != CAProduction && ca != CAStaging {
		return fmt.Errorf("CA must be %q or %q", CAProduction, CAStaging)
	}
	return nil
}

func validateCredential(key, value string) error {
	if !credentialRE.MatchString(key) {
		return fmt.Errorf("invalid credential environment variable %q", key)
	}
	if blockedCredentialNames[key] {
		return fmt.Errorf("environment variable %q cannot be set", key)
	}
	for _, prefix := range blockedCredentialPrefixes {
		if strings.HasPrefix(key, prefix) {
			return fmt.Errorf("environment variable %q cannot be set", key)
		}
	}
	if len(value) > 32*1024 || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("invalid value for %q", key)
	}
	return nil
}

func normalizeProfile(profile CertificateProfile) (CertificateProfile, error) {
	profile.ID = strings.ToLower(strings.TrimSpace(profile.ID))
	profile.Name = strings.TrimSpace(profile.Name)
	if !profileIDRE.MatchString(profile.ID) {
		return CertificateProfile{}, fmt.Errorf("ID must be 1-32 lowercase letters, digits, or hyphens")
	}
	if profile.Name == "" {
		profile.Name = profile.ID
	}
	if len(profile.Name) > 64 {
		return CertificateProfile{}, fmt.Errorf("name must be at most 64 characters")
	}
	if len(profile.Domains) == 0 || len(profile.Domains) > 20 {
		return CertificateProfile{}, fmt.Errorf("provide between 1 and 20 certificate domains")
	}
	seenDomains := map[string]bool{}
	domains := make([]string, 0, len(profile.Domains))
	for _, domain := range profile.Domains {
		domain, err := normalizeCertificateDomain(domain)
		if err != nil {
			return CertificateProfile{}, err
		}
		if !seenDomains[domain] {
			seenDomains[domain] = true
			domains = append(domains, domain)
		}
	}
	profile.Domains = domains

	if profile.KeyType == "" {
		profile.KeyType = "EC256"
	}
	profile.KeyType = strings.ToUpper(strings.TrimSpace(profile.KeyType))
	validKeyTypes := map[string]bool{"EC256": true, "EC384": true, "RSA2048": true, "RSA3072": true, "RSA4096": true}
	if !validKeyTypes[profile.KeyType] {
		return CertificateProfile{}, fmt.Errorf("unsupported key type %q", profile.KeyType)
	}
	if profile.RenewBeforeDays == 0 {
		profile.RenewBeforeDays = 30
	}
	if profile.RenewBeforeDays < 7 || profile.RenewBeforeDays > 60 {
		return CertificateProfile{}, fmt.Errorf("renew-before days must be between 7 and 60")
	}

	seenMappings := map[string]bool{}
	mappings := make([]LocalMapping, 0, len(profile.Mappings))
	for _, mapping := range profile.Mappings {
		mapping, err := normalizeLocalMapping(mapping)
		if err != nil {
			return CertificateProfile{}, err
		}
		if !hostnameCoveredByDomains(mapping.Hostname, profile.Domains) {
			return CertificateProfile{}, fmt.Errorf("hostname %q is not covered by this certificate", mapping.Hostname)
		}
		if seenMappings[mapping.Hostname] {
			return CertificateProfile{}, fmt.Errorf("duplicate local hostname %q", mapping.Hostname)
		}
		seenMappings[mapping.Hostname] = true
		mappings = append(mappings, mapping)
	}
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].Hostname < mappings[j].Hostname })
	profile.Mappings = mappings
	return profile, nil
}

func normalizeCertificateDomain(domain string) (string, error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	wildcard := strings.HasPrefix(domain, "*.")
	base := strings.TrimPrefix(domain, "*.")
	if domain == "" || len(domain) > 253 || net.ParseIP(base) != nil || !strings.Contains(base, ".") {
		return "", fmt.Errorf("invalid public DNS name %q", domain)
	}
	for _, label := range strings.Split(base, ".") {
		if !dnsLabelRE.MatchString(label) {
			return "", fmt.Errorf("invalid public DNS name %q", domain)
		}
	}
	for _, suffix := range []string{".lan", ".local", ".home", ".internal", ".localhost", ".localdomain", ".test", ".invalid", ".example", ".arpa", ".onion", ".alt"} {
		if strings.HasSuffix("."+base, suffix) {
			return "", fmt.Errorf("%q is not a publicly issuable DNS name", domain)
		}
	}
	if wildcard {
		return "*." + base, nil
	}
	return base, nil
}

func normalizeLocalMapping(mapping LocalMapping) (LocalMapping, error) {
	hostname, err := normalizeCertificateDomain(mapping.Hostname)
	if err != nil || strings.HasPrefix(hostname, "*.") {
		return LocalMapping{}, fmt.Errorf("invalid local hostname %q", mapping.Hostname)
	}
	ip := net.ParseIP(strings.TrimSpace(mapping.IPAddress))
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return LocalMapping{}, fmt.Errorf("invalid local IP address %q", mapping.IPAddress)
	}
	if !ip.IsPrivate() && !isCGNAT(ip) {
		return LocalMapping{}, fmt.Errorf("local mapping IP %q is not private", mapping.IPAddress)
	}
	return LocalMapping{Hostname: hostname, IPAddress: ip.String()}, nil
}

func isCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

func hostnameCoveredByDomains(hostname string, domains []string) bool {
	for _, domain := range domains {
		if hostname == domain {
			return true
		}
		if !strings.HasPrefix(domain, "*.") {
			continue
		}
		base := strings.TrimPrefix(domain, "*.")
		if strings.HasSuffix(hostname, "."+base) && strings.Count(hostname, ".") == strings.Count(base, ".")+1 {
			return true
		}
	}
	return false
}
