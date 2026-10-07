package modules

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

func TestFragmentedFrameAndPing(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	r := &discordRPC{connection: a}
	done := make(chan error, 1)
	go func() {
		body := []byte(`{"evt":"READY"}`)
		h := make([]byte, 8)
		binary.LittleEndian.PutUint32(h, 1)
		binary.LittleEndian.PutUint32(h[4:], uint32(len(body)))
		for _, v := range append(h, body...) {
			if _, e := b.Write([]byte{v}); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	value, err := r.receive(time.Second)
	if err != nil || value["evt"] != "READY" {
		t.Fatal(value, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	go func() {
		h := make([]byte, 8)
		binary.LittleEndian.PutUint32(h, 3)
		binary.LittleEndian.PutUint32(h[4:], 2)
		b.Write(append(h, []byte(`{}`)...))
		reply := make([]byte, 10)
		_, e := io.ReadFull(b, reply)
		if e == nil && binary.LittleEndian.Uint32(reply) != 4 {
			t.Error("pong missing")
		}
		done <- e
	}()
	if _, err = r.receive(time.Second); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestFrameLimitsErrorsAndReadDeadline(t *testing.T) {
	for _, test := range []struct {
		body string
		size uint32
	}{{`{"evt":"ERROR","data":{"message":"rejected"}}`, 0}, {"", 1024*1024 + 1}} {
		a, b := net.Pipe()
		r := &discordRPC{connection: a}
		go func() {
			defer b.Close()
			h := make([]byte, 8)
			binary.LittleEndian.PutUint32(h, 1)
			size := test.size
			if size == 0 {
				size = uint32(len(test.body))
			}
			binary.LittleEndian.PutUint32(h[4:], size)
			b.Write(append(h, []byte(test.body)...))
		}()
		if _, err := r.receive(time.Second); err == nil {
			t.Fatal("invalid frame accepted")
		}
		a.Close()
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	r := &discordRPC{connection: a}
	start := time.Now()
	v, e := r.receive(10 * time.Millisecond)
	if e != nil || v != nil || time.Since(start) > time.Second {
		t.Fatal("read unbounded", e)
	}
}

var rpcReadTimeout = &net.DNSError{IsTimeout: true}

type recordingRPCConnection struct {
	data          bytes.Buffer
	writes, reads int
}

func (c *recordingRPCConnection) Read([]byte) (int, error) { c.reads++; return 0, rpcReadTimeout }
func (c *recordingRPCConnection) Write(body []byte) (int, error) {
	c.writes++
	return c.data.Write(body)
}
func (c *recordingRPCConnection) Close() error                     { return nil }
func (c *recordingRPCConnection) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *recordingRPCConnection) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *recordingRPCConnection) SetDeadline(time.Time) error      { return nil }
func (c *recordingRPCConnection) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingRPCConnection) SetWriteDeadline(time.Time) error { return nil }

func TestCachedRPCActivityDeduplicationHeartbeatAndClear(t *testing.T) {
	connection := &recordingRPCConnection{}
	r := &discordRPC{connection: connection, app: "123"}
	activity := `{"details":"Movie","timestamps":{"start":123}}`
	r.update("123", activity)
	if connection.writes != 1 {
		t.Fatal("initial activity missing")
	}
	frame := connection.data.Bytes()
	var command map[string]any
	if err := json.Unmarshal(frame[8:], &command); err != nil {
		t.Fatal(err)
	}
	value, ok := command["args"].(map[string]any)["activity"].(map[string]any)
	if !ok || value["details"] != "Movie" {
		t.Fatal("cached JSON sent as string rather than object", command)
	}
	for i := 0; i < 100; i++ {
		r.update("123", activity)
	}
	if connection.writes != 1 || connection.reads != 101 {
		t.Fatal("duplicate sent or connection checks skipped", connection.writes, connection.reads)
	}
	r.timeSent = time.Now().Add(-31 * time.Second)
	r.update("123", activity)
	if connection.writes != 2 {
		t.Fatal("heartbeat resend missing")
	}
	r.update("123", "")
	if connection.writes != 3 || r.last != "null" || r.status != "Connected — presence cleared" {
		t.Fatal("clear failed")
	}
	r.close()
	r.connection = connection
	r.update("123", "null")
	if connection.writes != 4 {
		t.Fatal("clear was not replayed on reconnect")
	}
}

func BenchmarkRPCUnchangedActivity(b *testing.B) {
	activity := `{"details":"Movie","timestamps":{"start":123}}`
	r := &discordRPC{connection: &recordingRPCConnection{}, app: "123", last: activity, timeSent: time.Now()}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.update("123", activity)
	}
}
