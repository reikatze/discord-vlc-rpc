package modules

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

type discordRPC struct {
	dial           func() (net.Conn, error)
	connection     net.Conn
	app            string
	last           string
	retry          time.Duration
	next, timeSent time.Time
	status         string
	nonce          uint64
}

func (r *discordRPC) close() {
	if r.connection != nil {
		r.connection.Close()
	}
	r.connection = nil
	r.last = ""
}
func (r *discordRPC) send(op uint32, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	header := make([]byte, 8)
	binary.LittleEndian.PutUint32(header, op)
	binary.LittleEndian.PutUint32(header[4:], uint32(len(body)))
	r.connection.SetWriteDeadline(time.Now().Add(time.Second))
	_, err = io.Copy(r.connection, bytesReader(append(header, body...)))
	return err
}

type byteReader struct{ b []byte }

func bytesReader(b []byte) *byteReader { return &byteReader{b} }
func (b *byteReader) Read(p []byte) (int, error) {
	if len(b.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.b)
	b.b = b.b[n:]
	return n, nil
}
func (r *discordRPC) receive(timeout time.Duration) (map[string]any, error) {
	r.connection.SetReadDeadline(time.Now().Add(timeout))
	header := make([]byte, 8)
	n, err := io.ReadFull(r.connection, header)
	if err != nil {
		var e net.Error
		if n == 0 && errors.As(err, &e) && e.Timeout() {
			return nil, nil
		}
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header[4:])
	if size > 1024*1024 {
		return nil, errors.New("Discord frame exceeds limit")
	}
	r.connection.SetReadDeadline(time.Now().Add(time.Second))
	body := make([]byte, size)
	if _, err = io.ReadFull(r.connection, body); err != nil {
		return nil, err
	}
	value := map[string]any{}
	if len(body) > 0 {
		if err = json.Unmarshal(body, &value); err != nil {
			return nil, err
		}
	}
	switch binary.LittleEndian.Uint32(header) {
	case 2:
		return nil, errors.New("Discord closed connection")
	case 3:
		if err = r.send(4, value); err != nil {
			return nil, err
		}
	}
	if text(value, "evt") == "ERROR" {
		return nil, fmt.Errorf("Discord rejected request: %v", value["data"])
	}
	return value, nil
}
func (r *discordRPC) fail(err error) {
	r.close()
	r.status = "Disconnected: " + err.Error()
	if r.retry == 0 {
		r.retry = time.Second
	}
	r.next = time.Now().Add(r.retry)
	r.retry *= 2
	if r.retry > 60*time.Second {
		r.retry = 60 * time.Second
	}
}
func (r *discordRPC) update(app string, activity map[string]any) {
	if app != "" && app != r.app {
		r.close()
		r.app = app
		r.next = time.Time{}
		r.retry = time.Second
	}
	if r.app == "" {
		r.status = "Waiting for Discord application ID"
		return
	}
	if r.connection == nil {
		if time.Now().Before(r.next) {
			return
		}
		dial := r.dial
		if dial == nil {
			dial = connectDiscord
		}
		c, err := dial()
		if err != nil {
			r.fail(err)
			return
		}
		r.connection = c
		if err = r.send(0, map[string]any{"v": 1, "client_id": r.app}); err != nil {
			r.fail(err)
			return
		}
		ready := false
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			v, e := r.receive(200 * time.Millisecond)
			if e != nil {
				err = e
				break
			}
			if text(v, "evt") == "READY" {
				ready = true
				break
			}
		}
		if !ready {
			if err == nil {
				err = errors.New("Discord handshake timed out")
			}
			r.fail(err)
			return
		}
		r.retry = time.Second
		r.last = ""
	}
	for i := 0; i < 8; i++ {
		v, err := r.receive(time.Millisecond)
		if err != nil {
			r.fail(err)
			return
		}
		if v == nil {
			break
		}
	}
	body, _ := json.Marshal(activity)
	key := string(body)
	if key != r.last || time.Since(r.timeSent) > 30*time.Second {
		r.nonce++
		err := r.send(1, map[string]any{"cmd": "SET_ACTIVITY", "args": map[string]any{"pid": os.Getpid(), "activity": activity}, "nonce": fmt.Sprint(r.nonce)})
		if err != nil {
			r.fail(err)
			return
		}
		r.last = key
		r.timeSent = time.Now()
	}
	r.status = "Connected — presence cleared"
	if activity != nil {
		r.status = "Connected — sharing playback"
	}
}
