package tdl

import "syscall"

// hideWindowAttr 返回 Windows 下隐藏子进程控制台的 SysProcAttr。
func hideWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
