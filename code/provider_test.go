package main

import "testing"

func TestParseProviderInfo(t *testing.T) {
	input := `Configuration for Cloudflare.
Code: 'cloudflare'

Credentials:
  - "CF_DNS_API_TOKEN": API token with DNS edit permissions

Additional Configuration:
  - "CLOUDFLARE_TTL": The TTL of the TXT record

More information: https://go-acme.github.io/lego/dns/cloudflare
`
	info, err := parseProviderInfo("cloudflare", input)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Cloudflare" || len(info.Credentials) != 1 || info.Credentials[0].Name != "CF_DNS_API_TOKEN" {
		t.Fatalf("unexpected provider info: %#v", info)
	}
	if len(info.Configuration) != 1 || info.Documentation == "" {
		t.Fatalf("missing optional metadata: %#v", info)
	}
}

func TestProviderCredentialAllowlist(t *testing.T) {
	info := ProviderInfo{
		Code:          "cloudflare",
		Credentials:   []ProviderField{{Name: "CF_DNS_API_TOKEN"}},
		Configuration: []ProviderField{{Name: "CLOUDFLARE_TTL"}},
	}
	if err := validateProviderCredentialUpdate(info, AccountUpdate{Credentials: map[string]string{
		"CF_DNS_API_TOKEN": "token", "CLOUDFLARE_TTL": "120",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := validateProviderCredentialUpdate(info, AccountUpdate{Credentials: map[string]string{
		"HTTPS_PROXY": "https://attacker.invalid",
	}}); err == nil {
		t.Fatal("unsupported child-process environment variable was accepted")
	}
}
