package main

import (
	"syscall"
	"unsafe"

	"encoding/binary"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/ndbeals/winssh-pageant/internal/security"
	"github.com/ndbeals/winssh-pageant/internal/sshagent"
)

var (
	modkernel32          = syscall.NewLazyDLL("kernel32.dll")
	procOpenFileMappingA = modkernel32.NewProc("OpenFileMappingA")
)

const (
	// windows consts
	FILE_MAP_ALL_ACCESS = 0xf001f

	// Pageant consts
	agentMaxMessageLength = 1<<14 - 1
	agentCopyDataID       = 0x804e50ba
	wndClassName          = "Pageant"
)

// copyDataStruct is used to pass data in the WM_COPYDATA message.
// We directly pass a pointer to our copyDataStruct type, be careful that it matches the Windows type exactly
type copyDataStruct struct {
	dwData uintptr
	cbData uint32
	lpData uintptr
}

func registerPageantWindow(hInstance win.HINSTANCE) (atom win.ATOM) {
	var wc win.WNDCLASSEX
	wc.Style = 0

	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = syscall.NewCallback(wndProc)
	wc.CbClsExtra = 0
	wc.CbWndExtra = 0
	wc.HInstance = hInstance
	wc.HIcon = win.LoadIcon(0, win.MAKEINTRESOURCE(win.IDI_APPLICATION))
	wc.HCursor = win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_IBEAM))
	wc.HbrBackground = win.GetSysColorBrush(win.BLACK_BRUSH)
	wc.LpszMenuName = nil
	wc.LpszClassName = syscall.StringToUTF16Ptr(wndClassName)
	wc.HIconSm = win.LoadIcon(0, win.MAKEINTRESOURCE(win.IDI_APPLICATION))

	return win.RegisterClassEx(&wc)
}

func openFileMap(dwDesiredAccess uint32, bInheritHandle uint32, mapNamePtr uintptr) (windows.Handle, error) {
	mapPtr, _, err := procOpenFileMappingA.Call(uintptr(dwDesiredAccess), uintptr(bInheritHandle), mapNamePtr)

	if err != nil && err.Error() == "The operation completed successfully." {
		err = nil
	}

	return windows.Handle(mapPtr), err
}

func wndProc(hWnd win.HWND, message uint32, wParam uintptr, lParam uintptr) uintptr {
	switch message {
	case win.WM_COPYDATA:
		{
			copyData := (*copyDataStruct)(unsafe.Pointer(lParam))

			fileMap, err := openFileMap(FILE_MAP_ALL_ACCESS, 0, copyData.lpData)
			defer windows.CloseHandle(fileMap)

			// check security
			ourself, err := security.GetUserSID()
			if err != nil {
				return 0
			}
			ourself2, err := security.GetDefaultSID()
			if err != nil {
				return 0
			}
			mapOwner, err := security.GetHandleSID(fileMap)
			if err != nil {
				return 0
			}
			if !windows.EqualSid(mapOwner, ourself) && !windows.EqualSid(mapOwner, ourself2) {
				return 0
			}

			// Passed security checks, copy data
			sharedMemory, err := windows.MapViewOfFile(fileMap, 2, 0, 0, 0)
			if err != nil {
				return 0
			}
			defer windows.UnmapViewOfFile(sharedMemory)

			sharedMemoryArray := (*[agentMaxMessageLength]byte)(unsafe.Pointer(sharedMemory))

			size := binary.BigEndian.Uint32(sharedMemoryArray[:4]) + 4
			// size += 4
			if size > agentMaxMessageLength {
				return 0
			}

			result, err := sshagent.QueryAgent(*sshPipe, sharedMemoryArray[:size], agentMaxMessageLength)
			copy(sharedMemoryArray[:], result)
			// success
			return 1
		}
	}

	return win.DefWindowProc(hWnd, message, wParam, lParam)
}