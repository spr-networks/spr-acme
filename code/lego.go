package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args []string, env []string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	output := &boundedBuffer{max: 256 * 1024}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	return output.Bytes(), err
}

type boundedBuffer struct {
	data []byte
	max  int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		b.data = append([]byte{}, b.data[len(b.data)-b.max:]...)
	}
	return original, nil
}

func (b *boundedBuffer) Bytes() []byte { return append([]byte{}, b.data...) }

type JobState struct {
	Running     bool
	Phase       string
	StartedAt   string
	FinishedAt  string
	LastSuccess string
	LastError   string
}

type CertificateStatus struct {
	Profile          CertificateProfile
	Issued           bool
	NotBefore        string
	NotAfter         string
	DaysRemaining    int
	Issuer           string
	SerialNumber     string
	CertificateNames []string
	ExportPath       string
	Job              JobState
	MappingSyncError string
}

type Manager struct {
	store  *Store
	runner CommandRunner
	spr    *SPRClient

	operationMu sync.Mutex
	stateMu     sync.RWMutex
	jobs        map[string]JobState
	logs        map[string]string
	syncErrors  map[string]string
}

func NewManager(store *Store, runner CommandRunner, spr *SPRClient) *Manager {
	return &Manager{
		store: store, runner: runner, spr: spr,
		jobs: map[string]JobState{}, logs: map[string]string{}, syncErrors: map[string]string{},
	}
}

func baseLegoEnvironment(credentials map[string]string) []string {
	env := []string{
		"HOME=" + HomePath,
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"SSL_CERT_DIR=/etc/ssl/certs",
	}
	keys := make([]string, 0, len(credentials))
	for key := range credentials {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+credentials[key])
	}
	return env
}

func (m *Manager) StartIssue(id string, force bool) error {
	profile, ok := m.store.Profile(id)
	if !ok {
		return errNotFound
	}
	account := m.store.Account()
	if err := validateAccountFields(account.Email, account.Provider, account.CA); err != nil {
		return err
	}
	if !account.AcceptTOS {
		return fmt.Errorf("accept the Let's Encrypt terms of service before issuing")
	}
	if _, err := normalizeProfile(profile); err != nil {
		return err
	}

	m.stateMu.Lock()
	if m.jobs[id].Running {
		m.stateMu.Unlock()
		return fmt.Errorf("certificate operation already running")
	}
	job := m.jobs[id]
	job.Running = true
	job.Phase = "queued"
	job.StartedAt = time.Now().UTC().Format(time.RFC3339)
	job.FinishedAt = ""
	job.LastError = ""
	m.jobs[id] = job
	m.stateMu.Unlock()

	go m.runIssue(profile, account, force)
	return nil
}

func (m *Manager) runIssue(profile CertificateProfile, account AccountConfig, force bool) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()

	m.stateMu.Lock()
	job := m.jobs[profile.ID]
	job.Phase = "issuing"
	m.jobs[profile.ID] = job
	m.stateMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	args := []string{
		"run",
		"--path", LegoPath,
		"--server", caDirectoryURL(account.CA),
		"--email", account.Email,
		"--accept-tos",
		"--dns", account.Provider,
		"--key-type", profile.KeyType,
		"--cert.name", profile.ID,
		"--renew-days", strconv.Itoa(profile.RenewBeforeDays),
		"--force-cert-domains",
		"--no-bundle",
	}
	if force {
		args = append(args, "--renew-force", "--no-random-sleep")
	}
	for _, domain := range profile.Domains {
		args = append(args, "--domains", domain)
	}

	output, err := m.runner.Run(ctx, LegoBinary, args, baseLegoEnvironment(account.Credentials))
	cleanOutput := sanitizeOutput(string(output), account.Credentials)
	if err == nil {
		err = exportCertificate(profile.ID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.stateMu.Lock()
	job = m.jobs[profile.ID]
	job.Running = false
	job.Phase = "idle"
	job.FinishedAt = now
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			job.LastError = "certificate operation timed out after 20 minutes"
		} else {
			job.LastError = err.Error()
		}
	} else {
		job.LastError = ""
		job.LastSuccess = now
	}
	m.jobs[profile.ID] = job
	m.logs[profile.ID] = cleanOutput
	m.stateMu.Unlock()
}

func caDirectoryURL(ca string) string {
	if ca == CAProduction {
		return LetsEncryptProductionURL
	}
	return LetsEncryptStagingURL
}

func sanitizeOutput(output string, credentials map[string]string) string {
	values := make([]string, 0, len(credentials))
	for _, value := range credentials {
		if value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		output = strings.ReplaceAll(output, value, "[REDACTED]")
	}
	if len(output) > 128*1024 {
		output = output[len(output)-128*1024:]
	}
	return strings.TrimSpace(output)
}

func exportCertificate(id string) error {
	sourceDir := filepath.Join(LegoPath, "certificates")
	cert, err := os.ReadFile(filepath.Join(sourceDir, id+".crt"))
	if err != nil {
		return fmt.Errorf("read issued certificate: %w", err)
	}
	chain, err := os.ReadFile(filepath.Join(sourceDir, id+".issuer.crt"))
	if err != nil {
		return fmt.Errorf("read issuer certificate: %w", err)
	}
	key, err := os.ReadFile(filepath.Join(sourceDir, id+".key"))
	if err != nil {
		return fmt.Errorf("read private key: %w", err)
	}

	dir := filepath.Join(CertificatesPath, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fullchain := append(ensureTrailingNewline(cert), ensureTrailingNewline(chain)...)
	files := []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"cert.pem", cert, 0o644},
		{"chain.pem", chain, 0o644},
		{"fullchain.pem", fullchain, 0o644},
		{"privkey.pem", key, 0o600},
	}
	for _, file := range files {
		if err := atomicWriteFile(filepath.Join(dir, file.name), file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

func ensureTrailingNewline(data []byte) []byte {
	result := append([]byte{}, data...)
	if len(result) > 0 && result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	return result
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func readCertificateStatus(profile CertificateProfile) CertificateStatus {
	status := CertificateStatus{
		Profile:          profile,
		ExportPath:       filepath.Join(CertificatesPath, profile.ID),
		CertificateNames: []string{},
	}
	data, err := os.ReadFile(filepath.Join(status.ExportPath, "cert.pem"))
	if err != nil {
		return status
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return status
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return status
	}
	status.Issued = true
	status.NotBefore = cert.NotBefore.UTC().Format(time.RFC3339)
	status.NotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
	status.DaysRemaining = int(time.Until(cert.NotAfter).Hours() / 24)
	status.Issuer = cert.Issuer.CommonName
	status.SerialNumber = cert.SerialNumber.Text(16)
	status.CertificateNames = append([]string{}, cert.DNSNames...)
	return status
}

func (m *Manager) Status(id string) (CertificateStatus, bool) {
	profile, ok := m.store.Profile(id)
	if !ok {
		return CertificateStatus{}, false
	}
	status := readCertificateStatus(profile)
	m.stateMu.RLock()
	status.Job = m.jobs[id]
	status.MappingSyncError = m.syncErrors[id]
	m.stateMu.RUnlock()
	return status, true
}

func (m *Manager) Statuses() []CertificateStatus {
	profiles := m.store.Profiles()
	statuses := make([]CertificateStatus, 0, len(profiles))
	for _, profile := range profiles {
		status, _ := m.Status(profile.ID)
		statuses = append(statuses, status)
	}
	return statuses
}

func (m *Manager) Log(id string) (string, bool) {
	if _, ok := m.store.Profile(id); !ok {
		return "", false
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.logs[id], true
}

func (m *Manager) SetSyncError(id string, err error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if err == nil {
		delete(m.syncErrors, id)
	} else {
		m.syncErrors[id] = err.Error()
	}
}

func (m *Manager) RenewLoop(ctx context.Context) {
	syncTicker := time.NewTicker(5 * time.Minute)
	renewTimer := time.NewTimer(time.Minute)
	defer syncTicker.Stop()
	defer renewTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-syncTicker.C:
			m.SyncAllMappings()
		case <-renewTimer.C:
			for _, profile := range m.store.Profiles() {
				if profile.AutoRenew {
					_ = m.StartIssue(profile.ID, false)
				}
			}
			renewTimer.Reset(12 * time.Hour)
		}
	}
}
