package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var (
	pluginVersion = "dev"
	legoVersion   = "unknown"
)

func pluginSocketPath() (string, error) {
	path := os.Getenv("SPR_KRUN_PLUGIN_SOCKET")
	if path == "" {
		path = UnixPluginSocket
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("plugin socket path must be absolute: %q", path)
	}
	return path, nil
}

func listenUnix(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return net.Listen("unix", path)
}

func main() {
	if err := os.MkdirAll(StateDir, 0o700); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(ConfigFile), 0o700); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(LegoPath, 0o700); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(HomePath, 0o700); err != nil {
		log.Fatal(err)
	}
	unixPluginSocket, err := pluginSocketPath()
	if err != nil {
		log.Fatal(err)
	}

	store := NewStore(ConfigFile)
	if err := store.Load(); err != nil {
		log.Fatal("load config: ", err)
	}
	manager := NewManager(store, ExecRunner{}, NewSPRClient())

	listener, err := listenUnix(unixPluginSocket)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.Chmod(unixPluginSocket, 0o770); err != nil {
		// Docker Desktop bind mounts may not implement chmod on Unix sockets.
		// The startup umask is 0077, so continuing leaves a restrictive socket;
		// native Linux installs still receive the intended group permission.
		log.Printf("warning: chmod plugin socket: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.RenewLoop(ctx)

	server := &http.Server{
		Handler:           newRouter(store, manager),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	log.Printf("spr-acme %s (lego %s) listening on %s", pluginVersion, legoVersion, unixPluginSocket)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-signals:
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server failed: %v", err)
		}
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	_ = os.Remove(unixPluginSocket)
}
