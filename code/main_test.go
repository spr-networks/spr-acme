package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPluginSocketPathDefaultsToStateMount(t *testing.T) {
	t.Setenv("SPR_KRUN_PLUGIN_SOCKET", "")

	path, err := pluginSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != UnixPluginSocket {
		t.Fatalf("plugin socket path = %q, want %q", path, UnixPluginSocket)
	}
}

func TestPluginSocketPathUsesKrunOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "spr-acme.sock")
	t.Setenv("SPR_KRUN_PLUGIN_SOCKET", path)

	got, err := pluginSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("plugin socket path = %q, want %q", got, path)
	}
}

func TestPluginSocketPathRejectsRelativeOverride(t *testing.T) {
	t.Setenv("SPR_KRUN_PLUGIN_SOCKET", "spr-acme.sock")

	if _, err := pluginSocketPath(); err == nil {
		t.Fatal("pluginSocketPath accepted a relative override")
	}
}

func TestListenUnixCreatesSocketParent(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "acme-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "run", "spr-krun-plugin", "spr-acme.sock")

	listener, err := listenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(path)
	})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("plugin socket was not created: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("plugin socket has mode %v, want a Unix socket", info.Mode())
	}
}
