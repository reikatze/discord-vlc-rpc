//go:build windows

package modules

import (
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"
)

func findVLC() string {
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, key := range []string{`SOFTWARE\VideoLAN\VLC`, `SOFTWARE\WOW6432Node\VideoLAN\VLC`} {
			k, e := registry.OpenKey(root, key, registry.QUERY_VALUE)
			if e == nil {
				v, _, e := k.GetStringValue("InstallDir")
				k.Close()
				if e == nil && exists(v) {
					return v
				}
			}
		}
	}
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		p := filepath.Join(base, "VideoLAN/VLC")
		if exists(p) {
			return p
		}
	}
	return ""
}

func runningVLCProcesses() ([]vlcProcess, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	owner, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	var found []vlcProcess
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for e := windows.Process32First(snapshot, &entry); e == nil; e = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "vlc.exe") {
			continue
		}
		h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if e != nil {
			continue
		}
		var token windows.Token
		e = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token)
		if e != nil {
			windows.CloseHandle(h)
			continue
		}
		user, e := token.GetTokenUser()
		token.Close()
		if e != nil || !owner.User.Sid.Equals(user.User.Sid) {
			windows.CloseHandle(h)
			continue
		}
		buf := make([]uint16, 32768)
		size := uint32(len(buf))
		e = windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
		if e == nil {
			p := vlcProcess{pid: int(entry.ProcessID), exe: windows.UTF16ToString(buf[:size])}
			// ProcessCommandLineInformation returns a local UNICODE_STRING and its buffer.
			raw := make([]byte, 131072)
			var length uint32
			if windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&raw[0]), uint32(len(raw)), &length) == nil {
				text := (*windows.NTUnicodeString)(unsafe.Pointer(&raw[0]))
				start := uintptr(unsafe.Pointer(text.Buffer))
				base := uintptr(unsafe.Pointer(&raw[0]))
				if text.Length%2 == 0 && start >= base && start-base+uintptr(text.Length) <= uintptr(len(raw)) {
					p.args, _ = windows.DecomposeCommandLine(text.String())
				}
				runtime.KeepAlive(raw)
			}
			found = append(found, p)
		}
		windows.CloseHandle(h)
	}
	return found, nil
}
