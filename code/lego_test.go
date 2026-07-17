package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeLegoRunner struct {
	mu   sync.Mutex
	args []string
	env  []string
}

func (f *fakeLegoRunner) Run(_ context.Context, _ string, args []string, env []string) ([]byte, error) {
	f.mu.Lock()
	f.args = append([]string{}, args...)
	f.env = append([]string{}, env...)
	f.mu.Unlock()
	dir := filepath.Join(LegoPath, "certificates")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "home.crt"), []byte("CERT\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "home.issuer.crt"), []byte("CHAIN\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "home.key"), []byte("very-secret KEY\n"), 0o600)
	return []byte("provider response included very-secret"), nil
}

func TestManagerIssuesExportsAndRedacts(t *testing.T) {
	oldLegoPath, oldCertificatesPath, oldBinary := LegoPath, CertificatesPath, LegoBinary
	root := t.TempDir()
	LegoPath = filepath.Join(root, "lego")
	CertificatesPath = filepath.Join(root, "exports")
	LegoBinary = "/fake/lego"
	t.Cleanup(func() {
		LegoPath, CertificatesPath, LegoBinary = oldLegoPath, oldCertificatesPath, oldBinary
	})

	store := NewStore(filepath.Join(root, "config.json"))
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAccount(AccountUpdate{
		Email: "admin@example.com", Provider: "cloudflare", CA: CAStaging, AcceptTOS: true,
		Credentials: map[string]string{"CF_DNS_API_TOKEN": "very-secret"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProfile(CertificateProfile{ID: "home", Domains: []string{"home.example.com"}}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeLegoRunner{}
	manager := NewManager(store, runner, &SPRClient{})
	if err := manager.StartIssue("home", true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, _ := manager.Status("home")
		if !status.Job.Running && status.Job.FinishedAt != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, _ := manager.Status("home")
	if status.Job.Running || status.Job.LastError != "" {
		t.Fatalf("unexpected job state: %#v", status.Job)
	}
	fullchain, err := os.ReadFile(filepath.Join(CertificatesPath, "home", "fullchain.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(fullchain) != "CERT\nCHAIN\n" {
		t.Fatalf("unexpected fullchain: %q", fullchain)
	}
	keyInfo, err := os.Stat(filepath.Join(CertificatesPath, "home", "privkey.pem"))
	if err != nil || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode: %v %v", keyInfo, err)
	}
	logOutput, _ := manager.Log("home")
	if strings.Contains(logOutput, "very-secret") || !strings.Contains(logOutput, "[REDACTED]") {
		t.Fatalf("secret was not redacted: %q", logOutput)
	}
	runner.mu.Lock()
	joinedArgs := strings.Join(runner.args, " ")
	joinedEnv := strings.Join(runner.env, "\n")
	runner.mu.Unlock()
	for _, expected := range []string{"run", "--dns cloudflare", "--cert.name home", "--renew-force", "--no-bundle"} {
		if !strings.Contains(joinedArgs, expected) {
			t.Errorf("missing %q in %q", expected, joinedArgs)
		}
	}
	if !strings.Contains(joinedEnv, "CF_DNS_API_TOKEN=very-secret") {
		t.Fatal("provider credential was not passed in the environment")
	}
}

func TestSanitizeOutputRedactsShortCredentials(t *testing.T) {
	got := sanitizeOutput("token=x", map[string]string{"API_TOKEN": "x"})
	if strings.Contains(got, "token=x") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("short credential was not redacted: %q", got)
	}
}
