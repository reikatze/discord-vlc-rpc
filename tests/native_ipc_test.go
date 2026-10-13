package modules

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const integrationApplicationID = "123456789"

func requireDesktopIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("DISCORD_VLC_RPC_INTEGRATION") != "1" {
		t.Skip("set DISCORD_VLC_RPC_INTEGRATION=1 to require native IPC and real VLC tests")
	}
}

type ipcMockFrame struct {
	opcode  uint32
	payload map[string]any
}
type discordIPCMock struct {
	listener    net.Listener
	frames      chan ipcMockFrame
	failures    chan error
	ready       bool
	mu          sync.Mutex
	closed      bool
	connections map[net.Conn]bool
	wg          sync.WaitGroup
}

func newDiscordIPCMock(t *testing.T, listener net.Listener, ready bool) *discordIPCMock {
	t.Helper()
	m := &discordIPCMock{listener: listener, ready: ready, frames: make(chan ipcMockFrame, 128), failures: make(chan error, 16), connections: make(map[net.Conn]bool)}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			m.mu.Lock()
			if m.closed {
				m.mu.Unlock()
				c.Close()
				return
			}
			m.connections[c] = true
			m.wg.Add(1)
			m.mu.Unlock()
			go m.serve(c)
		}
	}()
	t.Cleanup(func() {
		m.mu.Lock()
		m.closed = true
		listener.Close()
		for c := range m.connections {
			c.Close()
		}
		m.mu.Unlock()
		done := make(chan struct{})
		go func() { m.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("native IPC mock did not stop")
		}
	})
	return m
}
func (m *discordIPCMock) fail(err error) {
	select {
	case m.failures <- err:
	default:
	}
}
func readIPCMockFrame(c net.Conn) (ipcMockFrame, error) {
	var header [8]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		return ipcMockFrame{}, err
	}
	size := binary.LittleEndian.Uint32(header[4:])
	if size > 1024*1024 {
		return ipcMockFrame{}, errors.New("oversized mock IPC frame")
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(c, body); err != nil {
		return ipcMockFrame{}, err
	}
	frame := ipcMockFrame{opcode: binary.LittleEndian.Uint32(header[:]), payload: make(map[string]any)}
	return frame, json.Unmarshal(body, &frame.payload)
}
func writeIPCMockFrame(c net.Conn, opcode uint32, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var header [8]byte
	binary.LittleEndian.PutUint32(header[:], opcode)
	binary.LittleEndian.PutUint32(header[4:], uint32(len(body)))
	c.SetWriteDeadline(time.Now().Add(time.Second))
	_, err = io.Copy(c, bytes.NewReader(append(header[:], body...)))
	return err
}
func (m *discordIPCMock) serve(c net.Conn) {
	defer m.wg.Done()
	defer func() { c.Close(); m.mu.Lock(); delete(m.connections, c); m.mu.Unlock() }()
	c.SetReadDeadline(time.Now().Add(30 * time.Second))
	first, err := readIPCMockFrame(c)
	if err != nil {
		m.fail(err)
		return
	}
	if first.opcode != 0 || first.payload["client_id"] != integrationApplicationID || number(first.payload, "v") != 1 {
		m.fail(fmt.Errorf("unexpected native IPC handshake: %v", first))
		return
	}
	m.frames <- first
	if m.ready {
		if err := writeIPCMockFrame(c, 1, map[string]any{"evt": "READY"}); err != nil {
			m.fail(err)
			return
		}
	}
	for {
		c.SetReadDeadline(time.Now().Add(30 * time.Second))
		frame, err := readIPCMockFrame(c)
		if err != nil {
			return
		}
		select {
		case m.frames <- frame:
		default:
			m.fail(errors.New("mock IPC frame queue full"))
			return
		}
	}
}
func (m *discordIPCMock) disconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for c := range m.connections {
		c.Close()
	}
}
func (m *discordIPCMock) await(t *testing.T, predicate func(ipcMockFrame) bool) ipcMockFrame {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case err := <-m.failures:
			t.Fatal("native IPC mock:", err)
		case frame := <-m.frames:
			if predicate(frame) {
				return frame
			}
		case <-timer.C:
			t.Fatal("expected native Discord IPC frame was not received")
			return ipcMockFrame{}
		}
	}
}
func mockActivity(frame ipcMockFrame) (map[string]any, bool) {
	if frame.opcode != 1 || frame.payload["cmd"] != "SET_ACTIVITY" {
		return nil, false
	}
	args, ok := frame.payload["args"].(map[string]any)
	if !ok {
		return nil, false
	}
	value, present := args["activity"]
	if !present {
		return nil, false
	}
	activity, _ := value.(map[string]any)
	return activity, true
}

// The harness itself is exercised in normal tests without native IPC privileges.
func TestDiscordIPCMockHarness(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	testDiscordIPCExchange(t, listener, func() (net.Conn, error) { return net.DialTimeout("tcp", listener.Addr().String(), time.Second) })
}
func TestNativeDiscordIPCExchange(t *testing.T) {
	requireDesktopIntegration(t)
	testDiscordIPCExchange(t, nativeDiscordListener(t), nil)
}
func testDiscordIPCExchange(t *testing.T, listener net.Listener, dial func() (net.Conn, error)) {
	m := newDiscordIPCMock(t, listener, true)
	r := &discordRPC{dial: dial}
	defer r.close()
	body, _ := json.Marshal(makeActivity(exampleSnapshot()))
	activity := string(body)
	r.update(integrationApplicationID, activity)
	m.await(t, func(f ipcMockFrame) bool { return f.opcode == 0 })
	m.await(t, func(f ipcMockFrame) bool { a, ok := mockActivity(f); return ok && a != nil && a["details"] == "Movie" })
	r.update(integrationApplicationID, activity)
	select {
	case frame := <-m.frames:
		t.Fatal("duplicate presence sent:", frame)
	case err := <-m.failures:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
	r.update(integrationApplicationID, "null")
	m.await(t, func(f ipcMockFrame) bool { a, ok := mockActivity(f); return ok && a == nil })
	m.disconnect()
	r.update(integrationApplicationID, activity)
	if r.connection != nil {
		t.Fatal("dropped native IPC connection was retained")
	}
	r.next = time.Time{} // Exercise reconnect immediately, without waiting for backoff.
	r.update(integrationApplicationID, activity)
	m.await(t, func(f ipcMockFrame) bool { return f.opcode == 0 })
	m.await(t, func(f ipcMockFrame) bool { a, ok := mockActivity(f); return ok && a != nil && a["details"] == "Movie" })
}
func TestDiscordIPCHandshakeDeadlineHarness(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	testDiscordIPCHandshakeDeadline(t, listener, func() (net.Conn, error) {
		return net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	})
}
func TestNativeDiscordIPCHandshakeDeadline(t *testing.T) {
	requireDesktopIntegration(t)
	testDiscordIPCHandshakeDeadline(t, nativeDiscordListener(t), nil)
}
func testDiscordIPCHandshakeDeadline(t *testing.T, listener net.Listener, dial func() (net.Conn, error)) {
	newDiscordIPCMock(t, listener, false)
	r := &discordRPC{dial: dial}
	defer r.close()
	start := time.Now()
	r.update(integrationApplicationID, "null")
	if time.Since(start) > 4*time.Second || r.connection != nil || !strings.Contains(r.status, "handshake timed out") {
		t.Fatal("native handshake deadline failed:", time.Since(start), r.status)
	}
}
