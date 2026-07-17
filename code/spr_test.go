package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSyncProfileMappingsUsesScopedSPRAPI(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	var mutations []sprMappingMutation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer scoped-token" {
			t.Fatalf("unexpected authorization header")
		}
		if r.URL.Path == "/dns/hostnames" {
			t.Fatalf("collection endpoint must not be used")
		}
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Method == http.MethodGet {
			http.Error(w, "hostname mapping not found", http.StatusNotFound)
			return
		}
		var mapping sprMappingMutation
		_ = json.NewDecoder(r.Body).Decode(&mapping)
		mu.Lock()
		mutations = append(mutations, mapping)
		mu.Unlock()
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mapping)
	}))
	defer server.Close()

	tokenPath := filepath.Join(t.TempDir(), "api-token")
	if err := os.WriteFile(tokenPath, []byte("scoped-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &SPRClient{BaseURL: server.URL, TokenFile: tokenPath, Client: server.Client()}
	manager := NewManager(NewStore(filepath.Join(t.TempDir(), "config.json")), &fakeLegoRunner{}, client)
	err := manager.SyncProfileMappings("home",
		[]LocalMapping{{Hostname: "old.example.com", IPAddress: "192.168.2.10"}},
		[]LocalMapping{{Hostname: "new.example.com", IPAddress: "192.168.2.20"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 || calls[0] != "GET /dns/hostnames/new.example.com" || calls[1] != "PUT /dns/hostnames/new.example.com" || calls[2] != "DELETE /dns/hostnames/old.example.com" {
		t.Fatalf("unexpected calls: %#v", calls)
	}
	if !mutations[0].CreateOnly || mutations[0].PreviousIPAddress != "" {
		t.Fatalf("new mapping was not create-only: %#v", mutations[0])
	}
	if mutations[1].IPAddress != "192.168.2.10" {
		t.Fatalf("delete did not carry the expected IP: %#v", mutations[1])
	}
}

func TestSyncProfileMappingsDoesNotOverwriteForeignMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected mutating request: %s", r.Method)
		}
		if r.URL.Path != "/dns/hostnames/vault.example.com" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(LocalMapping{Hostname: "vault.example.com", IPAddress: "192.168.2.99"})
	}))
	defer server.Close()

	tokenPath := filepath.Join(t.TempDir(), "api-token")
	if err := os.WriteFile(tokenPath, []byte("scoped-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &SPRClient{BaseURL: server.URL, TokenFile: tokenPath, Client: server.Client()}
	manager := NewManager(NewStore(filepath.Join(t.TempDir(), "config.json")), &fakeLegoRunner{}, client)
	err := manager.SyncProfileMappings("home", nil,
		[]LocalMapping{{Hostname: "vault.example.com", IPAddress: "192.168.2.20"}},
	)
	if err == nil || !strings.Contains(err.Error(), "already maps it") {
		t.Fatalf("expected mapping conflict, got %v", err)
	}
}
