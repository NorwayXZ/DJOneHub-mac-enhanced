package main

import (
	"errors"
	"testing"
)

func TestFriendlyProxyErrorIsChinese(t *testing.T) {
	for in, want := range map[string]string{
		"recovery timed out: x":                                  "等待蜂窝网络恢复超时，模块可能仍在重新注册，请稍后点“检测出口”",
		`Get "https://myip.ipip.net": context deadline exceeded`: "蜂窝网络响应超时，信号可能不稳定，请稍后重试",
		"USB bulk write: LIBUSB_ERROR_NO_DEVICE":                 "模块 USB 连接中断，请检查数据线后重试",
		"未找到模块的蜂窝网卡，请检查模块是否已连接并完成上网初始化":                          "未找到模块的蜂窝网卡，请检查模块是否已连接并完成上网初始化",
	} {
		if got := friendlyProxyError(errors.New(in)); got != want {
			t.Fatalf("%q -> %q", in, got)
		}
	}
	if friendlyProxyError(nil) != "" {
		t.Fatal("nil error")
	}
}
