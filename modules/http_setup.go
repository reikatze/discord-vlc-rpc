package modules

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const maxVLCConfig = 4 * 1024 * 1024

func readVLCConfig(path string) ([]byte, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxVLCConfig+1))
	if len(b) > maxVLCConfig {
		return nil, errors.New("VLC settings file exceeds size limit")
	}
	return b, err
}
func vlcOptions(body []byte) map[string]string {
	options := map[string]string{}
	for _, line := range strings.Split(strings.TrimPrefix(string(body), "\ufeff"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if ok && key != "" && !strings.HasPrefix(key, "#") && !strings.HasPrefix(key, "[") {
			options[key] = value
		}
	}
	return options
}
func hasHTTP(value string) bool {
	for _, name := range strings.FieldsFunc(value, func(r rune) bool { return r == ':' || r == ',' }) {
		if name == "http" || name == "luahttp" {
			return true
		}
	}
	return false
}
func httpPort(options map[string]string) int {
	p, err := strconv.Atoi(strings.TrimSpace(options["http-port"]))
	if err != nil || p < 1 || p > 65535 {
		return 8080
	}
	return p
}
func httpConfigured(options map[string]string) bool {
	port := strings.TrimSpace(options["http-port"])
	host := strings.TrimSpace(options["http-host"])
	local := host == "" || host == "0.0.0.0" || host == "::" || host == "localhost" || host == "127.0.0.1" || host == "::1"
	return local && hasHTTP(options["extraintf"]) && options["http-password"] != "" && (port == "" || strconv.Itoa(httpPort(options)) == port)
}
func probeVLCHTTP(ctx context.Context, options map[string]string) error {
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	host := "127.0.0.1"
	if options["http-host"] == "::1" {
		host = "::1"
	}
	request, err := http.NewRequestWithContext(ctx, "GET", "http://"+net.JoinHostPort(host, strconv.Itoa(httpPort(options)))+"/requests/status.json", nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth("", options["http-password"])
	response, err := client.Do(request)
	if err != nil {
		return errors.New("VLC HTTP interface is not reachable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return errors.New("VLC HTTP authentication failed; restart VLC to use its saved password")
	}
	if response.StatusCode != http.StatusOK {
		return errors.New("VLC HTTP endpoint did not return playback status")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 32769))
	if err != nil || len(body) > 32768 {
		return errors.New("VLC HTTP status response is invalid")
	}
	var status struct {
		State   string `json:"state"`
		Version string `json:"version"`
		API     int    `json:"apiversion"`
	}
	if json.Unmarshal(body, &status) != nil || status.Version == "" || status.API < 1 {
		return errors.New("The local HTTP endpoint is not a VLC status interface")
	}
	switch status.State {
	case "playing", "paused", "stopped":
		return nil
	}
	return errors.New("VLC HTTP status response has no recognized playback state")
}
func updateVLCOptions(body []byte, updates map[string]string) []byte {
	bom := ""
	s := string(body)
	if strings.HasPrefix(s, "\ufeff") {
		bom = "\ufeff"
		s = strings.TrimPrefix(s, bom)
	}
	newline := "\n"
	if strings.Contains(s, "\r\n") {
		newline = "\r\n"
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if value, change := updates[key]; ok && change {
			if seen[key] {
				lines[i] = "# " + line
			} else {
				lines[i] = key + "=" + value
				seen[key] = true
			}
		}
	}
	for _, key := range []string{"extraintf", "http-host", "http-port", "http-password"} {
		if value, ok := updates[key]; ok && !seen[key] {
			lines = append(lines, key+"="+value)
		}
	}
	return []byte(bom + strings.Join(lines, newline) + newline)
}
func configureVLCHTTP(path string) (bool, error) {
	body, err := readVLCConfig(path)
	if err != nil {
		return false, err
	}
	options := vlcOptions(body)
	if httpConfigured(options) {
		return false, nil
	}
	password := options["http-password"]
	if password == "" {
		secret := make([]byte, 24)
		if _, err = rand.Read(secret); err != nil {
			return false, err
		}
		password = hex.EncodeToString(secret)
	}
	interfaces := options["extraintf"]
	if !hasHTTP(interfaces) {
		if interfaces != "" {
			interfaces += ":"
		}
		interfaces += "http"
	}
	port := httpPort(options)
	listener, e := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if e != nil {
		listener, e = net.Listen("tcp4", "127.0.0.1:0")
		if e != nil {
			return false, errors.New("No local port is available for VLC HTTP")
		}
		port = listener.Addr().(*net.TCPAddr).Port
	}
	listener.Close()
	next := updateVLCOptions(body, map[string]string{"extraintf": interfaces, "http-host": "127.0.0.1", "http-port": strconv.Itoa(port), "http-password": password})
	latest, err := readVLCConfig(path)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(body, latest) {
		return false, errors.New("VLC settings changed during setup; check again")
	}
	if len(body) > 0 {
		backup := path + ".http-backup-" + time.Now().Format("20060102-150405.000000000")
		if err = atomicWrite(backup, body, 0600); err != nil {
			return false, fmt.Errorf("Cannot back up VLC settings: %w", err)
		}
	}
	if err = atomicWrite(path, next, 0600); err != nil {
		return false, err
	}
	return true, nil
}

// Only called by the explicit tray action; never changes settings at startup.
func checkConfigureHTTP(ctx context.Context, p paths, progress func(string)) (string, error) {
	if !p.configExplicit && !p.configFileExplicit {
		current, err := discoverPaths(p)
		if err != nil {
			return "", err
		}
		if !samePath(current.vlcConfigFile(), p.vlcConfigFile(), runtime.GOOS) {
			return "", errors.New("VLC is using a different configuration profile; restart discord-vlc-rpc while VLC is running or select --config-dir / --config-file")
		}
	}
	path := p.vlcConfigFile()
	body, err := readVLCConfig(path)
	if err != nil {
		return "", err
	}
	options := vlcOptions(body)
	if err = probeVLCHTTP(ctx, options); err == nil {
		return "VLC HTTP interface is responding.", nil
	}
	if httpConfigured(options) {
		return "VLC HTTP is configured. " + err.Error() + ". Start or restart VLC, then check again.", nil
	}
	running, err := vlcRunning()
	if err != nil {
		return "", err
	}
	if running {
		progress("Close VLC to apply HTTP settings. Setup resumes automatically.")
		timer := time.NewTimer(5 * time.Minute)
		defer timer.Stop()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for running {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-timer.C:
				return "", errors.New("HTTP setup timed out waiting for VLC to close; select Check / Configure again")
			case <-tick.C:
				running, err = vlcRunning()
				if err != nil {
					return "", err
				}
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	changed, err := configureVLCHTTP(path)
	if err != nil {
		return "", err
	}
	if !changed {
		return "VLC HTTP is already configured. Start VLC, then check again.", nil
	}
	return "VLC HTTP configured for localhost. Start VLC, then check again.", nil
}
