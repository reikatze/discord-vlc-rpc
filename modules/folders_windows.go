package modules

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var shellExecuteFolder = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW")

func openFolderPlatform(path string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	folder, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Shell extensions may need COM; initialize and release it on the same thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	if err == nil || err == syscall.Errno(1) { // S_FALSE also requires CoUninitialize.
		defer windows.CoUninitialize()
	} else if err != syscall.Errno(0x80010106) { // RPC_E_CHANGED_MODE: COM already initialized.
		return fmt.Errorf("initialize folder opener: %w", err)
	}
	result, _, _ := shellExecuteFolder.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(folder)), 0, 0, windows.SW_SHOWNORMAL)
	runtime.KeepAlive(verb)
	runtime.KeepAlive(folder)
	if result <= 32 {
		return fmt.Errorf("open folder %q: Windows shell error %d", path, result)
	}
	return nil
}
