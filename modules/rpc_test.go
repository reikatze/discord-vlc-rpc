package modules

import (
	"encoding/binary"
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
