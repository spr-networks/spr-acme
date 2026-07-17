package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var TEST_PREFIX = os.Getenv("TEST_PREFIX")

var (
	ConfigFile       = TEST_PREFIX + "/configs/spr-acme/config.json"
	APITokenFile     = TEST_PREFIX + "/configs/spr-acme/api-token"
	StateDir         = TEST_PREFIX + "/state/plugins/spr-acme"
	HomePath         = StateDir + "/home"
	LegoPath         = StateDir + "/lego"
	CertificatesPath = StateDir + "/certificates"
	UnixPluginSocket = StateDir + "/socket"
	LegoBinary       = "/usr/local/bin/lego"
)

const (
	CAProduction = "production"
	CAStaging    = "staging"

	LetsEncryptProductionURL = "https://acme-v02.api.letsencrypt.org/directory"
	LetsEncryptStagingURL    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

type AccountConfig struct {
	Email       string
	Provider    string
	Credentials map[string]string
	CA          string
	AcceptTOS   bool
}

type LocalMapping struct {
	Hostname  string
	IPAddress string
}

type CertificateProfile struct {
	ID              string
	Name            string
	Domains         []string
	KeyType         string
	AutoRenew       bool
	RenewBeforeDays int
	Mappings        []LocalMapping
}

type Config struct {
	Account  AccountConfig
	Profiles []CertificateProfile
}

type AccountUpdate struct {
	Email            string
	Provider         string
	Credentials      map[string]string
	ClearCredentials []string
	CA               string
	AcceptTOS        bool
}

type AccountPublic struct {
	Email                 string
	Provider              string
	ConfiguredCredentials []string
	CA                    string
	AcceptTOS             bool
}

func defaultConfig() Config {
	return Config{
		Account: AccountConfig{
			Credentials: map[string]string{},
			CA:          CAStaging,
		},
		Profiles: []CertificateProfile{},
	}
}

type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func NewStore(path string) *Store {
	return &Store{path: path, cfg: defaultConfig()}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s.saveLocked()
		}
		return err
	}

	cfg := defaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if cfg.Account.Credentials == nil {
		cfg.Account.Credentials = map[string]string{}
	}
	if cfg.Account.CA == "" {
		cfg.Account.CA = CAStaging
	}
	if cfg.Profiles == nil {
		cfg.Profiles = []CertificateProfile{}
	}
	s.cfg = cfg
	return nil
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config.json.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
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
	return os.Rename(tmpName, s.path)
}

func publicAccount(account AccountConfig) AccountPublic {
	configured := make([]string, 0, len(account.Credentials))
	for key, value := range account.Credentials {
		if value != "" {
			configured = append(configured, key)
		}
	}
	sort.Strings(configured)
	return AccountPublic{
		Email:                 account.Email,
		Provider:              account.Provider,
		ConfiguredCredentials: configured,
		CA:                    account.CA,
		AcceptTOS:             account.AcceptTOS,
	}
}

func (s *Store) Account() AccountConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := s.cfg.Account
	result.Credentials = cloneStringMap(result.Credentials)
	return result
}

func (s *Store) PublicAccount() AccountPublic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return publicAccount(s.cfg.Account)
}

func (s *Store) UpdateAccount(update AccountUpdate) (AccountPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	account := s.cfg.Account
	account.Credentials = cloneStringMap(account.Credentials)
	update.Email = strings.TrimSpace(update.Email)
	update.Provider = strings.ToLower(strings.TrimSpace(update.Provider))
	update.CA = strings.ToLower(strings.TrimSpace(update.CA))
	if err := validateAccountFields(update.Email, update.Provider, update.CA); err != nil {
		return AccountPublic{}, err
	}
	if update.Provider != account.Provider {
		account.Credentials = map[string]string{}
	}
	if account.Credentials == nil {
		account.Credentials = map[string]string{}
	}
	for key, value := range update.Credentials {
		key = strings.TrimSpace(key)
		if err := validateCredential(key, value); err != nil {
			return AccountPublic{}, err
		}
		account.Credentials[key] = value
	}
	for _, key := range update.ClearCredentials {
		delete(account.Credentials, strings.TrimSpace(key))
	}
	account.Email = update.Email
	account.Provider = update.Provider
	account.CA = update.CA
	account.AcceptTOS = update.AcceptTOS
	oldAccount := s.cfg.Account
	s.cfg.Account = account
	if err := s.saveLocked(); err != nil {
		s.cfg.Account = oldAccount
		return AccountPublic{}, err
	}
	return publicAccount(account), nil
}

func (s *Store) Profiles() []CertificateProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneProfiles(s.cfg.Profiles)
}

func (s *Store) Profile(id string) (CertificateProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, profile := range s.cfg.Profiles {
		if profile.ID == id {
			return cloneProfile(profile), true
		}
	}
	return CertificateProfile{}, false
}

func (s *Store) CreateProfile(profile CertificateProfile) (CertificateProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	profile, err := normalizeProfile(profile)
	if err != nil {
		return CertificateProfile{}, err
	}
	for _, existing := range s.cfg.Profiles {
		if existing.ID == profile.ID {
			return CertificateProfile{}, fmt.Errorf("certificate profile %q already exists", profile.ID)
		}
	}
	if err := validateMappingOwnership(profile, s.cfg.Profiles, ""); err != nil {
		return CertificateProfile{}, err
	}
	oldProfiles := cloneProfiles(s.cfg.Profiles)
	s.cfg.Profiles = append(s.cfg.Profiles, profile)
	sort.Slice(s.cfg.Profiles, func(i, j int) bool { return s.cfg.Profiles[i].ID < s.cfg.Profiles[j].ID })
	if err := s.saveLocked(); err != nil {
		s.cfg.Profiles = oldProfiles
		return CertificateProfile{}, err
	}
	return cloneProfile(profile), nil
}

func (s *Store) UpdateProfile(id string, profile CertificateProfile) (CertificateProfile, CertificateProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	profile.ID = id
	profile, err := normalizeProfile(profile)
	if err != nil {
		return CertificateProfile{}, CertificateProfile{}, err
	}
	if err := validateMappingOwnership(profile, s.cfg.Profiles, id); err != nil {
		return CertificateProfile{}, CertificateProfile{}, err
	}
	for i, existing := range s.cfg.Profiles {
		if existing.ID != id {
			continue
		}
		old := cloneProfile(existing)
		s.cfg.Profiles[i] = profile
		if err := s.saveLocked(); err != nil {
			s.cfg.Profiles[i] = old
			return CertificateProfile{}, CertificateProfile{}, err
		}
		return old, cloneProfile(profile), nil
	}
	return CertificateProfile{}, CertificateProfile{}, fmt.Errorf("certificate profile %q not found", id)
}

func (s *Store) DeleteProfile(id string) (CertificateProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, profile := range s.cfg.Profiles {
		if profile.ID != id {
			continue
		}
		old := cloneProfile(profile)
		oldProfiles := cloneProfiles(s.cfg.Profiles)
		s.cfg.Profiles = append(s.cfg.Profiles[:i], s.cfg.Profiles[i+1:]...)
		if err := s.saveLocked(); err != nil {
			s.cfg.Profiles = oldProfiles
			return CertificateProfile{}, err
		}
		return old, nil
	}
	return CertificateProfile{}, fmt.Errorf("certificate profile %q not found", id)
}

func validateMappingOwnership(profile CertificateProfile, profiles []CertificateProfile, skipID string) error {
	for _, mapping := range profile.Mappings {
		for _, existing := range profiles {
			if existing.ID == skipID {
				continue
			}
			for _, owned := range existing.Mappings {
				if owned.Hostname == mapping.Hostname {
					return fmt.Errorf("hostname %q is already managed by profile %q", mapping.Hostname, existing.ID)
				}
			}
		}
	}
	return nil
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneProfile(profile CertificateProfile) CertificateProfile {
	profile.Domains = append([]string{}, profile.Domains...)
	profile.Mappings = append([]LocalMapping{}, profile.Mappings...)
	return profile
}

func cloneProfiles(profiles []CertificateProfile) []CertificateProfile {
	result := make([]CertificateProfile, len(profiles))
	for i, profile := range profiles {
		result[i] = cloneProfile(profile)
	}
	return result
}

var errNotFound = errors.New("not found")
