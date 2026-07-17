package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestRouterAcceptsSPRPluginPrefix(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "config.json"))
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, &fakeLegoRunner{}, &SPRClient{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/plugins/spr-acme/status", nil)

	newRouter(store, manager).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response overviewResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Account.CA != CAStaging {
		t.Fatalf("unexpected response: %#v", response)
	}
}
