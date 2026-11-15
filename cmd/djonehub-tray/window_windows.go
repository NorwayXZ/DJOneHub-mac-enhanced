//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The DJOneHub window is its own process (DJOneHub.exe --window), a native
// window around the WebView2 engine that ships with Windows, like the macOS
// app's main window. Closing it leaves the tray, service and proxies running.

const (
	windowMutex   = `Local\DJOneHubWindow`
	windowClass   = "DJOneHubWindowLink"
	wmCopyData    = 0x004A
	wmSetIcon     = 0x0080
	swRestore     = 9
	dwmDarkMode   = 20 // DWMWA_USE_IMMERSIVE_DARK_MODE
	findTimeoutMs = 3000
	hwndMessage   = ^uintptr(2) // HWND_MESSAGE (-3)
)

var (
	findWindow       = user32.NewProc("FindWindowExW")
	sendMessage      = user32.NewProc("SendMessageW")
	showWindow       = user32.NewProc("ShowWindow")
	isIconic         = user32.NewProc("IsIconic")
	getWindowThread  = user32.NewProc("GetWindowThreadProcessId")
	allowSetFg       = user32.NewProc("AllowSetForegroundWindow")
	dwmapi           = windows.NewLazySystemDLL("dwmapi.dll")
	dwmSetAttribute  = dwmapi.NewProc("DwmSetWindowAttribute")
	windowProcessMu  sync.Mutex
	windowProcessPID int
)

type copyDataStruct struct {
	Data  uintptr
	Bytes uint32
	Ptr   uintptr
}

// openWindow shows the DJOneHub window at url: an open window is brought to
// the front and moved to url, otherwise a new one starts.
func openWindow(url string) {
	if link, _, _ := findWindow.Call(hwndMessage, 0, uintptr(unsafe.Pointer(utf16(windowClass))), 0); link != 0 {
		var pid uint32
		getWindowThread.Call(link, uintptr(unsafe.Pointer(&pid)))
		allowSetFg.Call(uintptr(pid))
		u, _ := windows.UTF16FromString(url)
		cds := copyDataStruct{Data: 1, Bytes: uint32(len(u) * 2), Ptr: uintptr(unsafe.Pointer(&u[0]))}
		sendMessage.Call(link, wmCopyData, 0, uintptr(unsafe.Pointer(&cds)))
		return
	}
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "--window", url)
	if cmd.Start() == nil {
		windowProcessMu.Lock()
		windowProcessPID = cmd.Process.Pid
		windowProcessMu.Unlock()
		go cmd.Wait()
		return
	}
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
}

// closeWindow ends the window process (完全退出).
func closeWindow() {
	if link, _, _ := findWindow.Call(hwndMessage, 0, uintptr(unsafe.Pointer(utf16(windowClass))), 0); link != 0 {
		var pid uint32
		getWindowThread.Call(link, uintptr(unsafe.Pointer(&pid)))
		if p, err := os.FindProcess(int(pid)); err == nil {
			p.Kill()
		}
	}
}

func utf16(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

func systemDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
}

// runWindow is DJOneHub.exe --window: one window per signed-in user.
func runWindow(url string) {
	createMutex.Call(0, 0, uintptr(unsafe.Pointer(utf16(windowMutex))))
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		openWindow(url)
		return
	}
	profile := filepath.Join(os.Getenv("LOCALAPPDATA"), "DJOneHub", "webview")
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  profile,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "DJOneHub",
			Width:  1180,
			Height: 820,
			Center: true,
		},
	})
	if w == nil {
		// No WebView2 runtime: fall back to an Edge app window.
		openEdgeWindow(url)
		return
	}
	defer w.Destroy()
	hwnd := uintptr(w.Window())
	if icon != 0 {
		sendMessage.Call(hwnd, wmSetIcon, 0, uintptr(icon))
		sendMessage.Call(hwnd, wmSetIcon, 1, uintptr(icon))
	}
	dark := int32(0)
	if systemDark() {
		dark = 1
	}
	dwmSetAttribute.Call(hwnd, dwmDarkMode, uintptr(unsafe.Pointer(&dark)), 4)
	w.SetSize(920, 640, webview2.HintMin)

	// A message-only window receives "show this page" from the tray.
	instance, _, _ := getModuleHandle.Call(0)
	wc := wndClassEx{WndProc: windows.NewCallback(func(h windows.Handle, m uint32, wp, lp uintptr) uintptr {
		if m == wmCopyData {
			cds := *(**copyDataStruct)(unsafe.Pointer(&lp))
			if cds.Data == 1 && cds.Bytes > 0 {
				next := windows.UTF16ToString(unsafe.Slice(*(**uint16)(unsafe.Pointer(&cds.Ptr)), cds.Bytes/2))
				w.Dispatch(func() { w.Navigate(next) })
			}
			if r, _, _ := isIconic.Call(hwnd); r != 0 {
				showWindow.Call(hwnd, swRestore)
			}
			setForeground.Call(hwnd)
			return 1
		}
		r, _, _ := defWindowProc.Call(uintptr(h), uintptr(m), wp, lp)
		return r
	}), Instance: windows.Handle(instance), ClassName: utf16(windowClass)}
	wc.Size = uint32(unsafe.Sizeof(wc))
	registerClass.Call(uintptr(unsafe.Pointer(&wc)))
	createWindow.Call(0, uintptr(unsafe.Pointer(utf16(windowClass))), 0, 0, 0, 0, 0, 0, hwndMessage, 0, instance, 0)

	w.Navigate(url)
	w.Run()
}

func openEdgeWindow(url string) {
	profile := filepath.Join(os.Getenv("LOCALAPPDATA"), "DJOneHub", "window")
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA")} {
		if base == "" {
			continue
		}
		edge := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
		if _, err := os.Stat(edge); err != nil {
			continue
		}
		if exec.Command(edge, "--app="+url, "--user-data-dir="+profile, "--window-size=1180,820", "--no-first-run").Start() == nil {
			return
		}
	}
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
}
