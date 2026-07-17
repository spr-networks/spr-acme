package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type SPRClient struct {
	BaseURL   string
	TokenFile string
	Client    *http.Client
}

func NewSPRClient() *SPRClient {
	base := strings.TrimRight(os.Getenv("SPR_API_BASE"), "/")
	if base == "" {
		base = "http://127.0.0.1:80"
		if os.Getenv("VIRTUAL_SPR") != "1" {
			if gateway, err := defaultGateway(); err == nil {
				base = "http://" + net.JoinHostPort(gateway, "80")
			}
		}
	}
	return &SPRClient{
		BaseURL: base, TokenFile: APITokenFile,
		Client: &http.Client{Timeout: 15 * time.Second},
	}
}

func defaultGateway() (string, error) {
	output, err := exec.Command("ip", "route").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "default" && fields[1] == "via" {
			if net.ParseIP(fields[2]) != nil {
				return fields[2], nil
			}
		}
	}
	return "", fmt.Errorf("default gateway not found")
}

type sprStatusError struct {
	StatusCode int
	Message    string
}

type sprMappingMutation struct {
	IPAddress         string
	PreviousIPAddress string
	CreateOnly        bool
}

func (e *sprStatusError) Error() string {
	return fmt.Sprintf("SPR API status %d: %s", e.StatusCode, e.Message)
}

func (c *SPRClient) request(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	token, err := os.ReadFile(c.TokenFile)
	if err != nil || strings.TrimSpace(string(token)) == "" {
		return fmt.Errorf("SPR API token is not configured")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &sprStatusError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(data))}
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *SPRClient) putMapping(ctx context.Context, mapping LocalMapping, previousIPAddress string, createOnly bool) error {
	return c.request(ctx, http.MethodPut, mappingPath(mapping.Hostname), sprMappingMutation{
		IPAddress:         mapping.IPAddress,
		PreviousIPAddress: previousIPAddress, CreateOnly: createOnly,
	}, nil)
}

func mappingPath(hostname string) string {
	return "/dns/hostnames/" + url.PathEscape(hostname)
}

func (c *SPRClient) getMapping(ctx context.Context, hostname string) (LocalMapping, bool, error) {
	var mapping LocalMapping
	err := c.request(ctx, http.MethodGet, mappingPath(hostname), nil, &mapping)
	statusErr := new(sprStatusError)
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
		return LocalMapping{}, false, nil
	}
	if err != nil {
		return LocalMapping{}, false, err
	}
	return mapping, true, nil
}

func (c *SPRClient) deleteMapping(ctx context.Context, hostname, expectedIPAddress string) error {
	err := c.request(ctx, http.MethodDelete, mappingPath(hostname), sprMappingMutation{
		IPAddress: expectedIPAddress,
	}, nil)
	statusErr := new(sprStatusError)
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

func (m *Manager) SyncProfileMappings(id string, oldMappings, newMappings []LocalMapping) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	oldByName := make(map[string]LocalMapping, len(oldMappings))
	for _, mapping := range oldMappings {
		oldByName[mapping.Hostname] = mapping
	}
	newByName := map[string]LocalMapping{}
	for _, mapping := range newMappings {
		newByName[mapping.Hostname] = mapping
	}
	var syncErr error
	for _, mapping := range newMappings {
		current, exists, err := m.spr.getMapping(ctx, mapping.Hostname)
		if err != nil {
			syncErr = errors.Join(syncErr, fmt.Errorf("read %s: %w", mapping.Hostname, err))
			continue
		}
		if exists && current.IPAddress == mapping.IPAddress {
			continue
		}
		if exists {
			old, owned := oldByName[mapping.Hostname]
			if !owned || old.IPAddress != current.IPAddress {
				syncErr = errors.Join(syncErr, fmt.Errorf("set %s: SPR already maps it to %s", mapping.Hostname, current.IPAddress))
				continue
			}
		}
		previousIPAddress := ""
		if exists {
			previousIPAddress = current.IPAddress
		}
		if err := m.spr.putMapping(ctx, mapping, previousIPAddress, !exists); err != nil {
			syncErr = errors.Join(syncErr, fmt.Errorf("set %s: %w", mapping.Hostname, err))
		}
	}
	for _, mapping := range oldMappings {
		if _, keep := newByName[mapping.Hostname]; keep {
			continue
		}
		if err := m.spr.deleteMapping(ctx, mapping.Hostname, mapping.IPAddress); err != nil {
			syncErr = errors.Join(syncErr, fmt.Errorf("remove %s: %w", mapping.Hostname, err))
		}
	}
	m.SetSyncError(id, syncErr)
	return syncErr
}

func (m *Manager) SyncAllMappings() {
	for _, profile := range m.store.Profiles() {
		_ = m.SyncProfileMappings(profile.ID, nil, profile.Mappings)
	}
}
