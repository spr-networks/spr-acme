package main

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ProviderField struct {
	Name        string
	Description string
}

type ProviderInfo struct {
	Code          string
	Name          string
	Credentials   []ProviderField
	Configuration []ProviderField
	Documentation string
}

var providerFieldRE = regexp.MustCompile(`^\s*-\s+"([A-Z][A-Z0-9_]*)":\s*(.*)$`)

func (m *Manager) ProviderCodes() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := m.runner.Run(ctx, LegoBinary, []string{"dnshelp"}, baseLegoEnvironment(nil))
	if err != nil {
		return nil, fmt.Errorf("lego dnshelp: %w: %s", err, strings.TrimSpace(string(output)))
	}
	marker := "Supported DNS providers:"
	idx := strings.Index(string(output), marker)
	if idx < 0 {
		return nil, fmt.Errorf("unexpected lego dnshelp output")
	}
	raw := strings.TrimSpace(string(output)[idx+len(marker):])
	if info := strings.Index(raw, "More information:"); info >= 0 {
		raw = raw[:info]
	}
	var result []string
	for _, code := range strings.Split(raw, ",") {
		code = strings.TrimSpace(code)
		if providerRE.MatchString(code) && code != "manual" && code != "exec" {
			result = append(result, code)
		}
	}
	sort.Strings(result)
	return result, nil
}

func (m *Manager) ProviderInfo(code string) (ProviderInfo, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if !providerRE.MatchString(code) || code == "manual" || code == "exec" {
		return ProviderInfo{}, fmt.Errorf("invalid or unsupported provider code")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := m.runner.Run(ctx, LegoBinary, []string{"dnshelp", "-c", code}, baseLegoEnvironment(nil))
	if err != nil {
		return ProviderInfo{}, fmt.Errorf("lego dnshelp: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return parseProviderInfo(code, string(output))
}

// validateProviderCredentialUpdate limits the child-process environment to
// variables that the selected lego provider explicitly documents. This keeps
// unrelated process controls (for example proxy variables) out of the ACME
// client even if a caller bypasses the UI.
func validateProviderCredentialUpdate(info ProviderInfo, update AccountUpdate) error {
	allowed := make(map[string]bool, len(info.Credentials)+len(info.Configuration))
	for _, field := range append(append([]ProviderField{}, info.Credentials...), info.Configuration...) {
		allowed[field.Name] = true
	}
	for key := range update.Credentials {
		if !allowed[key] {
			return fmt.Errorf("credential variable %q is not supported by provider %q", key, info.Code)
		}
	}
	return nil
}

func parseProviderInfo(code, output string) (ProviderInfo, error) {
	info := ProviderInfo{Code: code}
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "Configuration for "):
			info.Name = strings.TrimSuffix(strings.TrimPrefix(line, "Configuration for "), ".")
		case line == "Credentials:":
			section = "credentials"
		case line == "Additional Configuration:":
			section = "configuration"
		case strings.HasPrefix(line, "More information:"):
			info.Documentation = strings.TrimSpace(strings.TrimPrefix(line, "More information:"))
			section = ""
		default:
			matches := providerFieldRE.FindStringSubmatch(scanner.Text())
			if len(matches) != 3 {
				continue
			}
			field := ProviderField{Name: matches[1], Description: strings.TrimSpace(matches[2])}
			if section == "credentials" {
				info.Credentials = append(info.Credentials, field)
			} else if section == "configuration" {
				info.Configuration = append(info.Configuration, field)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ProviderInfo{}, err
	}
	if info.Name == "" {
		return ProviderInfo{}, fmt.Errorf("unexpected lego provider help output")
	}
	return info, nil
}
