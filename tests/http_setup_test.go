package modules

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHTTPConfigurationPreservesSettingsAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vlcrc")
	original := []byte("\ufeff# VLC settings\r\n[main]\r\nvolume=73\r\nextraintf=rc\r\nfullscreen=1\r\nhttp-password=existing-secret\r\nhttp-port=0\r\n")
	os.WriteFile(path, original, 0600)
	changed, err := configureVLCHTTP(path)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	body, _ := os.ReadFile(path)
	options := vlcOptions(body)
	if options["extraintf"] != "rc:http" || options["volume"] != "73" || options["fullscreen"] != "1" || options["http-password"] != "existing-secret" || options["http-host"] != "127.0.0.1" {
		t.Fatal("existing settings not preserved")
	}
	if !bytes.HasPrefix(body, []byte("\ufeff")) || strings.Contains(strings.ReplaceAll(string(body), "\r\n", ""), "\n") {
		t.Fatal("BOM or line endings changed")
	}
	backups, _ := filepath.Glob(path + ".http-backup-*")
	if len(backups) != 1 {
		t.Fatal("backup missing")
	}
	backup, _ := os.ReadFile(backups[0])
	if !bytes.Equal(backup, original) {
		t.Fatal("backup differs")
	}
	changed, err = configureVLCHTTP(path)
	if err != nil || changed {
		t.Fatal("not idempotent", changed, err)
	}
	again, _ := os.ReadFile(path)
	if !bytes.Equal(again, body) {
		t.Fatal("configured profile modified")
	}
}
func TestHTTPFreshConfigurationAndDuplicateKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vlcrc")
	changed, err := configureVLCHTTP(path)
	if err != nil || !changed {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	options := vlcOptions(body)
	if !httpConfigured(options) || len(options["http-password"]) != 48 {
		t.Fatal("HTTP setup incomplete")
	}
	body = updateVLCOptions([]byte("extraintf=rc\nextraintf=lua\nhttp-password=old\nhttp-password=new\n"), map[string]string{"extraintf": "lua:http", "http-password": "retained"})
	options = vlcOptions(body)
	if options["extraintf"] != "lua:http" || options["http-password"] != "retained" {
		t.Fatal("duplicate keys override setup")
	}
}
func TestHTTPProbeAuthenticationAndIdentity(t *testing.T) {
	respond := `{"version":"3.0.24","apiversion":3,"state":"playing"}`
	authenticated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "" || password != "test-password" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/requests/status.json" {
			t.Error("wrong endpoint")
		}
		authenticated = true
		fmt.Fprint(w, respond)
	}))
	defer server.Close()
	port := strings.Split(server.URL, ":")[2]
	options := map[string]string{"http-port": port, "http-password": "test-password"}
	if err := probeVLCHTTP(context.Background(), options); err != nil || !authenticated {
		t.Fatal(err)
	}
	options["http-password"] = "wrong"
	if err := probeVLCHTTP(context.Background(), options); err == nil {
		t.Fatal("authentication failure accepted")
	}
	options["http-password"] = "test-password"
	respond = `{"state":"playing"}`
	if err := probeVLCHTTP(context.Background(), options); err == nil {
		t.Fatal("unrelated server accepted")
	}
}
func TestHTTPProbeRejectsRedirectsAndIsBounded(t *testing.T) {
	followed := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer server.Close()
	options := map[string]string{"http-port": strings.Split(server.URL, ":")[2]}
	if err := probeVLCHTTP(context.Background(), options); err == nil || followed {
		t.Fatal("followed redirect")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	options["http-port"] = strings.Split(slow.URL, ":")[2]
	start := time.Now()
	if err := probeVLCHTTP(ctx, options); err == nil || time.Since(start) > time.Second {
		t.Fatal("probe exceeded deadline")
	}
}
func TestHTTPInvalidPortsAndReadLimit(t *testing.T) {
	for _, s := range []string{"-1", "0", "65536", "bad"} {
		if httpConfigured(map[string]string{"extraintf": "http", "http-password": "secret", "http-port": s}) {
			t.Fatal("invalid port accepted", s)
		}
	}
	options := map[string]string{"extraintf": "luahttp", "http-password": "secret", "http-port": strconv.Itoa(12345)}
	if !httpConfigured(options) {
		t.Fatal("HTTP alias missing")
	}
	path := filepath.Join(t.TempDir(), "vlcrc")
	os.WriteFile(path, bytes.Repeat([]byte("x"), maxVLCConfig+1), 0600)
	if _, err := readVLCConfig(path); err == nil {
		t.Fatal("config read unbounded")
	}
}
