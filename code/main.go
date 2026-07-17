package main

import (
	"context"
	"errors"
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

	store := NewStore(ConfigFile)
	if err := store.Load(); err != nil {
		log.Fatal("load config: ", err)
	}
	manager := NewManager(store, ExecRunner{}, NewSPRClient())

	if err := os.Remove(UnixPluginSocket); err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}
	listener, err := net.Listen("unix", UnixPluginSocket)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.Chmod(UnixPluginSocket, 0o770); err != nil {
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
	log.Printf("spr-acme %s (lego %s) listening on %s", pluginVersion, legoVersion, UnixPluginSocket)

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
	_ = os.Remove(UnixPluginSocket)
}
