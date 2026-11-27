//go:build windows

package install

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows Shell's link object and the interfaces Inkuna uses on it.
var (
	clsidShellLink   = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLinkW    = windows.GUID{Data1: 0x000214F9, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPersistFile   = windows.GUID{Data1: 0x0000010B, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	coCreateInstance = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")
)

// Method indexes in the interfaces' vtables, after IUnknown's
// QueryInterface (0), AddRef (1), and Release (2).
const (
	shellLinkGetPath             = 3
	shellLinkSetWorkingDirectory = 9
	shellLinkSetIconLocation     = 17
	shellLinkSetPath             = 20
	persistFileLoad              = 5
	persistFileSave              = 6
	unknownQueryInterface        = 0
	unknownRelease               = 2
)

const (
	clsctxInprocServer   = 0x1
	coinitApartment      = 0x2
	hresultFalse         = 0x1        // S_FALSE: COM was already set up on this thread
	hresultChangedMode   = 0x80010106 // RPC_E_CHANGED_MODE: set up in another mode, still usable
	stgmRead             = 0x0
	shellLinkGetRawPath  = 0x4
	shellLinkPathMaxLong = 32768
)

// comObject is a COM interface pointer: a pointer to its vtable. The array
// is longer than any vtable used here; only the indexes above are read.
type comObject struct {
	vtable *[32]uintptr
}

// call calls a method of the interface. Like syscall's own calls, it is
// marked uintptrescapes, so a pointer converted to uintptr in its argument
// list is moved to the heap and kept alive until the call returns: Windows
// reads strings and writes results through them while Go's garbage
// collector and stack moves would otherwise not know they are in use. Every
// caller converts its pointers inside the call expression for that reason,
// and keeps the Go values alive past it with runtime.KeepAlive as well.
//
//go:uintptrescapes
func (object *comObject) call(method int, args ...uintptr) error {
	result, _, _ := syscall.SyscallN(object.vtable[method], append([]uintptr{uintptr(unsafe.Pointer(object))}, args...)...)
	if int32(result) < 0 {
		return windows.Errno(result)
	}
	return nil
}

func (object *comObject) release() { _ = object.call(unknownRelease) }

// withShellLink sets COM up on a locked thread, creates the Shell's link
// object, and passes it and its IPersistFile to use.
func withShellLink(use func(link, file *comObject) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch err := windows.CoInitializeEx(0, coinitApartment); {
	case err == nil, errors.Is(err, windows.Errno(hresultFalse)):
		defer windows.CoUninitialize()
	case errors.Is(err, windows.Errno(hresultChangedMode)):
	default:
		return fmt.Errorf("set up COM: %w", err)
	}
	var link *comObject
	// LazyProc.Call keeps the pointers converted in its argument list alive.
	result, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer, uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&link)))
	if int32(result) < 0 {
		return fmt.Errorf("create the Shell's link object: %w", windows.Errno(result))
	}
	defer link.release()
	var file *comObject
	err := link.call(unknownQueryInterface, uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&file)))
	runtime.KeepAlive(&file)
	if err != nil {
		return fmt.Errorf("open the link object as a file: %w", err)
	}
	defer file.release()
	return use(link, file)
}

// saveShortcut makes a shortcut at path to target with the Windows Shell's
// own link object (IShellLinkW), the documented way to make one. It is a
// direct call into the Shell, not a script, and the Shell writes every part
// of the format Explorer and the Start menu read.
func saveShortcut(path, target string) error {
	return withShellLink(func(link, file *comObject) error {
		for _, set := range []struct {
			method int
			value  string
			what   string
		}{
			{shellLinkSetPath, target, "target"},
			{shellLinkSetWorkingDirectory, filepath.Dir(target), "working folder"},
			{shellLinkSetIconLocation, target, "icon"},
		} {
			value, err := windows.UTF16PtrFromString(set.value)
			if err != nil {
				return err
			}
			if set.method == shellLinkSetIconLocation {
				// The first icon in the app.
				err = link.call(set.method, uintptr(unsafe.Pointer(value)), 0)
			} else {
				err = link.call(set.method, uintptr(unsafe.Pointer(value)))
			}
			runtime.KeepAlive(value)
			if err != nil {
				return fmt.Errorf("set the shortcut's %s: %w", set.what, err)
			}
		}
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		err = file.call(persistFileSave, uintptr(unsafe.Pointer(name)), 1)
		runtime.KeepAlive(name)
		if err != nil {
			return fmt.Errorf("save the shortcut: %w", err)
		}
		return nil
	})
}

// shortcutTarget reads where a shortcut points, as the Shell reads it.
func shortcutTarget(path string) (string, error) {
	var target string
	err := withShellLink(func(link, file *comObject) error {
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		err = file.call(persistFileLoad, uintptr(unsafe.Pointer(name)), stgmRead)
		runtime.KeepAlive(name)
		if err != nil {
			return fmt.Errorf("load the shortcut: %w", err)
		}
		buffer := make([]uint16, shellLinkPathMaxLong)
		err = link.call(shellLinkGetPath, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0, shellLinkGetRawPath)
		runtime.KeepAlive(buffer)
		if err != nil {
			return fmt.Errorf("read the shortcut's target: %w", err)
		}
		target = windows.UTF16ToString(buffer)
		return nil
	})
	return target, err
}
