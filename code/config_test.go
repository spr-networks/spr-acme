package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRedactsCredentialsAndPersists0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "config.json")
	store := NewStore(path)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	public, err := store.UpdateAccount(AccountUpdate{
		Email: "admin@example.com", Provider: "cloudflare", CA: CAStaging, AcceptTOS: true,
		Credentials: map[string]string{"CF_DNS_API_TOKEN": "super-secret-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(public.ConfiguredCredentials) != 1 || public.ConfiguredCredentials[0] != "CF_DNS_API_TOKEN" {
		t.Fatalf("unexpected public account: %#v", public)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "super-secret-token") {
		t.Fatal("credential was not persisted")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode is %o", info.Mode().Perm())
	}

	reloaded := NewStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if reloaded.Account().Credentials["CF_DNS_API_TOKEN"] != "super-secret-token" {
		t.Fatal("credential did not survive reload")
	}
}

func TestCredentialEnvironmentIsConstrained(t *testing.T) {
	for _, key := range []string{"LEGO_POST_HOOK", "PATH", "LD_PRELOAD", "HTTPS_PROXY", "bad-key"} {
		if err := validateCredential(key, "value"); err == nil {
			t.Errorf("expected %q to be rejected", key)
		}
	}
	if err := validateCredential("AWS_ACCESS_KEY_ID", "value"); err != nil {
		t.Fatalf("expected provider credential to be accepted: %v", err)
	}
}

func TestRejectedAccountUpdateDoesNotMutateCredentials(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAccount(AccountUpdate{
		Email: "admin@example.com", Provider: "cloudflare", CA: CAStaging,
		Credentials: map[string]string{"CF_DNS_API_TOKEN": "original"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAccount(AccountUpdate{
		Email: "admin@example.com", Provider: "cloudflare", CA: CAStaging,
		Credentials: map[string]string{"CF_DNS_API_TOKEN": "replacement", "HTTPS_PROXY": "invalid"},
	}); err == nil {
		t.Fatal("expected account update to fail")
	}
	if got := store.Account().Credentials["CF_DNS_API_TOKEN"]; got != "original" {
		t.Fatalf("rejected update changed stored credential to %q", got)
	}
}

func TestProfileValidationAndMappingOwnership(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateProfile(CertificateProfile{
		ID: "home-services", Domains: []string{"*.home.example.com", "home.example.com"},
		AutoRenew: true, Mappings: []LocalMapping{{Hostname: "vault.home.example.com", IPAddress: "192.168.2.20"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.KeyType != "EC256" || profile.RenewBeforeDays != 30 {
		t.Fatalf("defaults not applied: %#v", profile)
	}
	_, err = store.CreateProfile(CertificateProfile{
		ID: "duplicate", Domains: []string{"vault.home.example.com"},
		Mappings: []LocalMapping{{Hostname: "vault.home.example.com", IPAddress: "192.168.2.21"}},
	})
	if err == nil || !strings.Contains(err.Error(), "already managed") {
		t.Fatalf("expected ownership error, got %v", err)
	}
	if _, err := normalizeCertificateDomain("router.lan"); err == nil {
		t.Fatal("reserved local suffix accepted")
	}
	if _, err := normalizeCertificateDomain("router.home.arpa"); err == nil {
		t.Fatal("special-use ARPA suffix accepted")
	}
	if hostnameCoveredByDomains("nested.vault.home.example.com", []string{"*.home.example.com"}) {
		t.Fatal("wildcard unexpectedly covered multiple labels")
	}
}
