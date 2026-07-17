package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type profileResponse struct {
	Profile   CertificateProfile
	SyncError string
}

type overviewResponse struct {
	Version      string
	LegoVersion  string
	Account      AccountPublic
	Certificates []CertificateStatus
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func jsonError(w http.ResponseWriter, status int, err error) {
	jsonResponse(w, status, map[string]string{"Error": err.Error()})
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("request body must contain one JSON value")
	}
	return nil
}

func newRouter(store *Store, manager *Manager) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		jsonResponse(w, http.StatusOK, overviewResponse{
			Version: pluginVersion, LegoVersion: legoVersion,
			Account: store.PublicAccount(), Certificates: manager.Statuses(),
		})
	})
	mux.HandleFunc("GET /account", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		jsonResponse(w, http.StatusOK, store.PublicAccount())
	})
	mux.HandleFunc("PUT /account", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var update AccountUpdate
		if err := decodeJSON(r, &update); err != nil {
			jsonError(w, http.StatusBadRequest, err)
			return
		}
		if err := validateAccountFields(strings.TrimSpace(update.Email), strings.ToLower(strings.TrimSpace(update.Provider)), strings.ToLower(strings.TrimSpace(update.CA))); err != nil {
			jsonError(w, http.StatusBadRequest, err)
			return
		}
		provider, err := manager.ProviderInfo(update.Provider)
		if err != nil {
			jsonError(w, http.StatusBadRequest, fmt.Errorf("validate DNS provider: %w", err))
			return
		}
		if err := validateProviderCredentialUpdate(provider, update); err != nil {
			jsonError(w, http.StatusBadRequest, err)
			return
		}
		account, err := store.UpdateAccount(update)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err)
			return
		}
		jsonResponse(w, http.StatusOK, account)
	})

	mux.HandleFunc("GET /providers", func(w http.ResponseWriter, r *http.Request) {
		codes, err := manager.ProviderCodes()
		if err != nil {
			jsonError(w, http.StatusBadGateway, err)
			return
		}
		jsonResponse(w, http.StatusOK, codes)
	})
	mux.HandleFunc("GET /providers/{code}", func(w http.ResponseWriter, r *http.Request) {
		info, err := manager.ProviderInfo(r.PathValue("code"))
		if err != nil {
			jsonError(w, http.StatusBadRequest, err)
			return
		}
		jsonResponse(w, http.StatusOK, info)
	})

	mux.HandleFunc("GET /certificates", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		jsonResponse(w, http.StatusOK, manager.Statuses())
	})
	mux.HandleFunc("POST /certificates", func(w http.ResponseWriter, r *http.Request) {
		var profile CertificateProfile
		if err := decodeJSON(r, &profile); err != nil {
			jsonError(w, http.StatusBadRequest, err)
			return
		}
		profile, err := store.CreateProfile(profile)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err)
			return
		}
		syncErr := manager.SyncProfileMappings(profile.ID, nil, profile.Mappings)
		response := profileResponse{Profile: profile}
		if syncErr != nil {
			response.SyncError = syncErr.Error()
		}
		jsonResponse(w, http.StatusCreated, response)
	})
	mux.HandleFunc("PUT /certificates/{id}", func(w http.ResponseWriter, r *http.Request) {
		var profile CertificateProfile
		if err := decodeJSON(r, &profile); err != nil {
			jsonError(w, http.StatusBadRequest, err)
			return
		}
		old, updated, err := store.UpdateProfile(r.PathValue("id"), profile)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			jsonError(w, status, err)
			return
		}
		syncErr := manager.SyncProfileMappings(updated.ID, old.Mappings, updated.Mappings)
		response := profileResponse{Profile: updated}
		if syncErr != nil {
			response.SyncError = syncErr.Error()
		}
		jsonResponse(w, http.StatusOK, response)
	})
	mux.HandleFunc("DELETE /certificates/{id}", func(w http.ResponseWriter, r *http.Request) {
		profile, err := store.DeleteProfile(r.PathValue("id"))
		if err != nil {
			jsonError(w, http.StatusNotFound, err)
			return
		}
		syncErr := manager.SyncProfileMappings(profile.ID, profile.Mappings, nil)
		response := profileResponse{Profile: profile}
		if syncErr != nil {
			response.SyncError = syncErr.Error()
		}
		jsonResponse(w, http.StatusOK, response)
	})
	mux.HandleFunc("POST /certificates/{id}/issue", func(w http.ResponseWriter, r *http.Request) {
		force := r.URL.Query().Get("force") == "1" || strings.EqualFold(r.URL.Query().Get("force"), "true")
		err := manager.StartIssue(r.PathValue("id"), force)
		if err != nil {
			status := http.StatusConflict
			if errors.Is(err, errNotFound) {
				status = http.StatusNotFound
			} else if !strings.Contains(err.Error(), "already running") {
				status = http.StatusBadRequest
			}
			jsonError(w, status, err)
			return
		}
		status, _ := manager.Status(r.PathValue("id"))
		jsonResponse(w, http.StatusAccepted, status)
	})
	mux.HandleFunc("POST /certificates/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		profile, ok := store.Profile(r.PathValue("id"))
		if !ok {
			jsonError(w, http.StatusNotFound, errNotFound)
			return
		}
		if err := manager.SyncProfileMappings(profile.ID, nil, profile.Mappings); err != nil {
			jsonError(w, http.StatusBadGateway, err)
			return
		}
		status, _ := manager.Status(profile.ID)
		jsonResponse(w, http.StatusOK, status)
	})
	mux.HandleFunc("GET /certificates/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		output, ok := manager.Log(r.PathValue("id"))
		if !ok {
			jsonError(w, http.StatusNotFound, errNotFound)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"Output": output})
	})
	mux.HandleFunc("GET /certificates/{id}/files/{name}", func(w http.ResponseWriter, r *http.Request) {
		id, name := r.PathValue("id"), r.PathValue("name")
		if !profileIDRE.MatchString(id) {
			jsonError(w, http.StatusBadRequest, fmt.Errorf("invalid certificate ID"))
			return
		}
		allowed := map[string]bool{"cert.pem": true, "chain.pem": true, "fullchain.pem": true, "privkey.pem": true}
		if !allowed[name] {
			jsonError(w, http.StatusNotFound, errNotFound)
			return
		}
		path := filepath.Join(CertificatesPath, id, name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			jsonError(w, http.StatusNotFound, errNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s"`, id, name))
		w.Header().Set("Cache-Control", "no-store")
		file, err := os.Open(path)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err)
			return
		}
		defer file.Close()
		http.ServeContent(w, r, name, info.ModTime(), file)
	})

	mux.Handle("/", spaHandler{root: "/ui"})
	root := securityHeaders(logRequests(mux))

	// SPR normally removes the plugin prefix before proxying to this socket.
	// Also accepting the prefixed form makes direct local previews and health
	// checks exercise the exact URLs emitted by the bundled frontend.
	router := http.NewServeMux()
	router.Handle("/plugins/spr-acme/", http.StripPrefix("/plugins/spr-acme", root))
	router.Handle("/", root)
	return router
}

type spaHandler struct{ root string }

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clean := filepath.Clean("/" + r.URL.Path)
	path := filepath.Join(h.root, clean)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		http.ServeFile(w, r, path)
		return
	}
	http.ServeFile(w, r, filepath.Join(h.root, "index.html"))
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' data: blob:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}
