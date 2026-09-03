//go:build !windows

package tdl

import "syscall"

// hideWindowAttr 在非 Windows 平台返回 Setpgid 配置（便于整组终止）。
func hideWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
