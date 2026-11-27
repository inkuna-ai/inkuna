//go:build windows

package install

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// registryPath is the user PATH in the registry.
type registryPath struct{}

// SystemUserPath is this machine's user PATH: the Path value under
// HKEY_CURRENT_USER\Environment, which new terminals start with.
func SystemUserPath() UserPath { return registryPath{} }

func (registryPath) Get() (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer func() { _ = key.Close() }()
	value, _, err := key.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	return value, err
}

// Set writes the value as REG_EXPAND_SZ, as Windows does, so entries such
// as %USERPROFILE%\bin keep expanding, then tells running programs the
// environment changed so Explorer starts new terminals with it.
func (registryPath) Set(value string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer func() { _ = key.Close() }()
	if err := key.SetExpandStringValue("Path", value); err != nil {
		return err
	}
	broadcastEnvironmentChange()
	return nil
}

// broadcastEnvironmentChange sends WM_SETTINGCHANGE for "Environment". It
// is best effort: the PATH is already written, and a terminal opened after
// signing in again sees it either way.
func broadcastEnvironmentChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
		timeoutMillis   = 5000
	)
	environment, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	send := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	var result uintptr
	_, _, _ = send.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(environment)), smtoAbortIfHung, timeoutMillis, uintptr(unsafe.Pointer(&result)))
}
