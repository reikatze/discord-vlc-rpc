//go:build windows

package modules

import (
	"golang.org/x/sys/windows"
	"strings"
	"unsafe"
)

func vlcRunning() (bool, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	err = windows.Process32First(snapshot, &entry)
	for err == nil {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "vlc.exe") {
			return true, nil
		}
		err = windows.Process32Next(snapshot, &entry)
	}
	if err == windows.ERROR_NO_MORE_FILES {
		return false, nil
	}
	return false, err
}
