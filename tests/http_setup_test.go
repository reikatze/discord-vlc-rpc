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

func TestSharedVLCHTTPPoliciesPreserveLimitsAndErrors(t *testing.T) {
	valid := `{"version":"3.0.24","apiversion":3,"state":"playing"}`
	for _, test := range []struct {
		name                            string
		status                          int
		body, probeError, playbackError string
	}{
		{"valid", 200, valid, "", ""},
		{"authentication", 401, "", "VLC HTTP authentication failed; restart VLC to use its saved password", "VLC HTTP password rejected; restart VLC"},
		{"service error", 503, "", "VLC HTTP endpoint did not return playback status", "VLC HTTP status failed"},
		{"invalid JSON", 200, "{", "The local HTTP endpoint is not a VLC status interface", "Invalid VLC HTTP JSON"},
		{"probe limit", 200, valid + strings.Repeat(" ", 32768), "VLC HTTP status response is invalid", ""},
		{"playback limit", 200, valid + strings.Repeat(" ", 1024*1024), "VLC HTTP status response is invalid", "VLC HTTP response exceeds limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			options := map[string]string{"http-port": strings.Split(server.URL, ":")[2]}
			check := func(err error, want string) {
				t.Helper()
				if want == "" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if err == nil || err.Error() != want {
					t.Fatalf("error = %v, want %q", err, want)
				}
			}
			check(probeVLCHTTP(context.Background(), options), test.probeError)
			client := localHTTPClient()
			defer client.CloseIdleConnections()
			var status vlcStatus
			check(vlcHTTP(context.Background(), client, options, "status.json", &status), test.playbackError)
			if test.playbackError == "" && (!status.valid() || status.State != "playing") {
				t.Fatal("embedded VLC identity was not decoded", status)
			}
		})
	}
}
