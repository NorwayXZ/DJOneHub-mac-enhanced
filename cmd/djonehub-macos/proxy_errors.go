package main

import "strings"

// User-facing wording for busy states shared by every module operation.
const (
	errModuleBusy   = "模块正在执行其他操作（如换 IP、频段测试或初始化），请几秒后再试"
	errCallActive   = "正在通话中，请挂断后再试"
	errUpdateActive = "正在更新软件，请稍后再试"
)

// friendlyProxyError turns low-level network/AT errors into short Chinese
// explanations for the App; the original text still goes to the log.
func friendlyProxyError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	lower := strings.ToLower(text)
	switch {
	case strings.ContainsRune(text, '，') || strings.ContainsRune(text, '；'):
		return text // already Chinese
	case strings.Contains(lower, "recovery timed out"):
		return "等待蜂窝网络恢复超时，模块可能仍在重新注册，请稍后点“检测出口”"
	case strings.Contains(lower, "no_device") || strings.Contains(lower, "not connected") || strings.Contains(lower, "usb"):
		return "模块 USB 连接中断，请检查数据线后重试"
	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timeout"):
		return "蜂窝网络响应超时，信号可能不稳定，请稍后重试"
	case strings.Contains(lower, "cellular dns failed") || strings.Contains(lower, "no ipv4 address"):
		return "蜂窝 DNS 解析失败，网络可能还在恢复"
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "no route"):
		return "蜂窝网络暂时不可用，请稍后重试"
	case strings.Contains(lower, "exit lookup http") || strings.Contains(lower, "exit provider") || strings.Contains(lower, "public ipv4"):
		return "出口检测网站暂时无法访问，代理本身可能仍可用"
	case strings.Contains(lower, "address already in use"):
		return "代理端口已被其他程序占用"
	}
	return text
}
