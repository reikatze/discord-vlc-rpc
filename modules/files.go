package modules

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func readJSON(path string, limit int64, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > limit {
		return errors.New("file too large")
	}
	return json.Unmarshal(raw, value)
}
func atomicWrite(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".discord-vlc-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceFile(temp, path)
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// cachedFile is owned by one polling loop. File identity detects atomic replacements
// even when size and modification time are unchanged. Failed reads are cached too,
// so invalid settings keep reporting an error without being reparsed every tick.
type cachedFile[T any] struct {
	path   string
	load   func() (T, error)
	info   os.FileInfo
	loaded bool
	value  T
	err    error
}

func (c *cachedFile[T]) read(force bool) (T, error, bool) {
	info, err := os.Stat(c.path)
	if err != nil && !os.IsNotExist(err) {
		return c.value, err, false
	}
	same := c.loaded && ((info == nil && c.info == nil) ||
		(info != nil && c.info != nil && os.SameFile(info, c.info) &&
			info.Size() == c.info.Size() && info.ModTime().Equal(c.info.ModTime())))
	if force || !same {
		c.value, c.err = c.load()
		c.info, c.loaded = info, true
		return c.value, c.err, true
	}
	return c.value, c.err, false
}
