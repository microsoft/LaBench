package bench

import (
	"syscall"
	"unsafe"
)

func init() {
	ntdll := syscall.MustLoadDLL("ntdll.dll")
	setTimerResolution := ntdll.MustFindProc("NtSetTimerResolution")
	var prevRes int
	setTimerResolution.Call(5000, 1, uintptr(unsafe.Pointer(&prevRes)))
}
