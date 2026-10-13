package modules

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInstanceLockExclusionAndClose(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "config")
	first, err := acquireInstanceLock(folder)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := acquireInstanceLock(folder); !errors.Is(err, errInstanceRunning) {
		if second != nil {
			second.Close()
		}
		t.Fatal("duplicate instance was not rejected:", err)
	}
	// A failed acquisition must not release the owner's lock.
	if second, err := acquireInstanceLock(folder); !errors.Is(err, errInstanceRunning) {
		if second != nil {
			second.Close()
		}
		t.Fatal("failed contender released the lock:", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(folder, "instance.lock")); err != nil {
		t.Fatal("lock file should remain:", err)
	}
	second, err := acquireInstanceLock(folder)
	if err != nil {
		t.Fatal("lock not released by Close:", err)
	}
	second.Close()
	other, err := acquireInstanceLock(t.TempDir())
	if err != nil {
		t.Fatal("independent configuration folder blocked:", err)
	}
	other.Close()
	if _, err := acquireInstanceLock(""); err == nil {
		t.Fatal("empty configuration directory accepted")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireInstanceLock(blocked); err == nil || errors.Is(err, errInstanceRunning) {
		t.Fatal("filesystem failure mistaken for running instance:", err)
	}
}

func TestInstanceLockProcessHelper(t *testing.T) {
	folder := os.Getenv("DISCORD_VLC_RPC_TEST_LOCK_FOLDER")
	if folder == "" {
		return
	}
	lock, err := acquireInstanceLock(folder)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	fmt.Println("lock-ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestInstanceLockReleasedOnProcessExit(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprint("crash=", crash), func(t *testing.T) {
			folder := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInstanceLockProcessHelper$", "-test.count=1")
			cmd.Env = append(os.Environ(), "DISCORD_VLC_RPC_TEST_LOCK_FOLDER="+folder)
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				input.Close()
				if !waited {
					cmd.Process.Kill()
					cmd.Wait()
				}
			}()
			ready, err := bufio.NewReader(output).ReadString('\n')
			if err != nil || ready != "lock-ready\n" {
				t.Fatal("child failed to acquire lock:", ready, err)
			}
			if lock, err := acquireInstanceLock(folder); !errors.Is(err, errInstanceRunning) {
				if lock != nil {
					lock.Close()
				}
				t.Fatal("cross-process exclusion failed:", err)
			}
			if crash {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else {
				input.Close()
			}
			err = cmd.Wait()
			waited = true
			if !crash && err != nil {
				t.Fatal("child exit failed:", err)
			}
			lock, err := acquireInstanceLock(folder)
			if err != nil {
				t.Fatal("process exit left a stale lock:", err)
			}
			lock.Close()
		})
	}
}
