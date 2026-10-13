package modules

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func integrationVLCBinary(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("VLC_BINARY"); path != "" {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
		t.Fatal("VLC_BINARY does not point to a VLC executable:", path)
	}
	if path, err := exec.LookPath("vlc"); err == nil {
		return path
	}
	if runtime.GOOS == "darwin" {
		path := "/Applications/VLC.app/Contents/MacOS/VLC"
		if exists(path) {
			return path
		}
	}
	if runtime.GOOS == "windows" {
		path := filepath.Join(os.Getenv("ProgramFiles"), "VideoLAN", "VLC", "vlc.exe")
		if exists(path) {
			return path
		}
	}
	t.Fatal("integration mode requires VLC; install it or set VLC_BINARY")
	return ""
}

// A local, deterministic 180-second PCM WAV needs no codecs or generated fixtures
// in the repository, and lets VLC's clock advance with dummy audio output.
func writeIntegrationMedia(path string) error {
	const sampleRate = 8000
	const samples = sampleRate * 180
	body := make([]byte, 44+samples*2)
	copy(body, "RIFF")
	binary.LittleEndian.PutUint32(body[4:], uint32(len(body)-8))
	copy(body[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(body[16:], 16)
	binary.LittleEndian.PutUint16(body[20:], 1)
	binary.LittleEndian.PutUint16(body[22:], 1)
	binary.LittleEndian.PutUint32(body[24:], sampleRate)
	binary.LittleEndian.PutUint32(body[28:], sampleRate*2)
	binary.LittleEndian.PutUint16(body[32:], 2)
	binary.LittleEndian.PutUint16(body[34:], 16)
	copy(body[36:], "data")
	binary.LittleEndian.PutUint32(body[40:], samples*2)
	return os.WriteFile(path, body, 0600)
}

func TestRealVLCPlaybackToNativeDiscordIPC(t *testing.T) {
	requireDesktopIntegration(t)
	binary := integrationVLCBinary(t)
	p := testPaths(t)
	cfg := defaults()
	cfg.ApplicationID = integrationApplicationID
	cfg.IndexEnabled = false
	if err := saveSettings(p, cfg); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(t.TempDir(), "CI Playback.wav")
	if err := writeIntegrationMedia(media); err != nil {
		t.Fatal(err)
	}
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	options := map[string]string{"http-port": strconv.Itoa(port), "http-host": "127.0.0.1", "http-password": "ci-local-password"}
	if err := atomicWrite(p.vlcConfigFile(), []byte(fmt.Sprintf("extraintf=http\nhttp-host=127.0.0.1\nhttp-port=%d\nhttp-password=ci-local-password\n", port)), 0600); err != nil {
		reservation.Close()
		t.Fatal(err)
	}
	m := newDiscordIPCMock(t, nativeDiscordListener(t), true)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	vlcCtx, stopVLC := context.WithCancel(ctx)
	defer stopVLC()
	logPath := filepath.Join(t.TempDir(), "vlc.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		reservation.Close()
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.CommandContext(vlcCtx, binary, "--intf=dummy", "--extraintf=http", "--http-host=127.0.0.1", "--http-port="+strconv.Itoa(port), "--http-password=ci-local-password", "--aout=dummy", "--vout=dummy", "--no-video", "--no-one-instance", "--no-media-library", "--no-osd", "--config="+p.vlcConfigFile(), "--quiet", media)
	configureChild(cmd)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.WaitDelay = time.Second
	reservation.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal("start real VLC:", err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- cmd.Wait() }()
	defer func() {
		stopVLC()
		select {
		case <-processDone:
		case <-time.After(6 * time.Second):
			t.Error("VLC process did not stop")
		}
		if t.Failed() {
			if body, err := os.ReadFile(logPath); err == nil {
				if len(body) > 65536 {
					body = body[len(body)-65536:]
				}
				t.Logf("VLC process output:\n%s", body)
			}
		}
	}()
	client := localHTTPClient()
	defer client.CloseIdleConnections()
	awaitStatus := func(predicate func(vlcStatus) bool) vlcStatus {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		var last error
		for time.Now().Before(deadline) {
			var status vlcStatus
			last = vlcHTTP(ctx, client, options, "status.json", &status)
			if last == nil && status.valid() && predicate(status) {
				return status
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
		t.Fatal("real VLC did not reach the expected state:", last)
		return vlcStatus{}
	}
	awaitStatus(func(s vlcStatus) bool { return s.State == "playing" && s.Length >= 170 })
	if err := probeVLCHTTP(ctx, options); err != nil {
		t.Fatal("real VLC HTTP probe:", err)
	}
	wrong := map[string]string{"http-port": options["http-port"], "http-password": "incorrect"}
	if err := probeVLCHTTP(ctx, wrong); err == nil {
		t.Fatal("real VLC accepted an incorrect password")
	}
	s := &service{paths: p, refresh: make(chan struct{}, 1), buildIndex: make(chan struct{}, 1)}
	serviceCtx, stopService := context.WithCancel(ctx)
	serviceDone := make(chan error, 1)
	go func() { serviceDone <- s.run(serviceCtx) }()
	defer func() {
		stopService()
		select {
		case err := <-serviceDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(6 * time.Second):
			t.Error("companion service did not stop")
		}
	}()
	awaitActivity := func(predicate func(map[string]any) bool) map[string]any {
		t.Helper()
		frame := m.await(t, func(f ipcMockFrame) bool { a, ok := mockActivity(f); return ok && predicate(a) })
		a, _ := mockActivity(frame)
		return a
	}
	initial := awaitActivity(func(a map[string]any) bool {
		return a != nil && a["details"] == "CI Playback" && a["timestamps"] != nil
	})
	initialEnd := number(initial["timestamps"].(map[string]any), "end")
	control := func(command, val string) {
		t.Helper()
		query := url.Values{"command": {command}}
		if val != "" {
			query.Set("val", val)
		}
		var status vlcStatus
		if err := vlcHTTP(ctx, client, options, "status.json?"+query.Encode(), &status); err != nil {
			t.Fatal("VLC playback control:", err)
		}
		poke(s.refresh)
	}
	control("pl_pause", "")
	awaitStatus(func(s vlcStatus) bool { return s.State == "paused" })
	awaitActivity(func(a map[string]any) bool { return a != nil && a["state"] == "Paused" && a["timestamps"] == nil })
	control("seek", "60")
	awaitStatus(func(s vlcStatus) bool { return s.State == "paused" && s.Time >= 59 && s.Time <= 61 })
	sampler := &playbackClient{}
	snapshot, parsed, err := sampler.sample(ctx, options, cfg)
	if err != nil || snapshot == nil || parsed.Title != "CI Playback" || number(snapshot.Playback, "position") < 59 {
		t.Fatal("real seek/media sampling failed:", snapshot, parsed, err)
	}
	control("pl_pause", "")
	awaitStatus(func(s vlcStatus) bool { return s.State == "playing" })
	awaitActivity(func(a map[string]any) bool {
		if a == nil || a["state"] == "Paused" {
			return false
		}
		stamps, ok := a["timestamps"].(map[string]any)
		return ok && number(stamps, "end") < initialEnd-40
	})
	control("pl_stop", "")
	awaitStatus(func(s vlcStatus) bool { return s.State == "stopped" })
	awaitActivity(func(a map[string]any) bool {
		return a != nil && a["details"] == "VLC" && a["state"] == "Idle" && a["timestamps"] == nil
	})
	cfg.Enabled = false
	if err := saveSettings(p, cfg); err != nil {
		t.Fatal(err)
	}
	poke(s.refresh)
	awaitActivity(func(a map[string]any) bool { return a == nil })
	// Re-enable playback and verify shutdown clears an active presence as well.
	cfg.Enabled = true
	if err := saveSettings(p, cfg); err != nil {
		t.Fatal(err)
	}
	control("pl_play", "")
	awaitActivity(func(a map[string]any) bool {
		return a != nil && a["details"] == "CI Playback" && a["timestamps"] != nil
	})
	stopService()
	m.await(t, func(f ipcMockFrame) bool {
		a, ok := mockActivity(f)
		return ok && a == nil && f.payload["nonce"] == "shutdown"
	})
}
