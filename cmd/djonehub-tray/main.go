//go:build windows

// DJOneHub.exe is the Windows desktop side, like the macOS menu bar app: a
// tray icon that opens the DJOneHub window and shows a notification for each
// new SMS and incoming call. The modules and proxies run in the DJOneHub
// service, so closing the window or quitting the tray changes nothing there.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	appURL      = "http://127.0.0.1:7575/app/"
	trayMessage = 0x8000 + 1 // WM_APP + 1
	menuOpen    = 1
	menuQuit    = 2

	nimAdd       = 0
	nimModify    = 1
	nimDelete    = 2
	nifMessage   = 0x1
	nifIcon      = 0x2
	nifTip       = 0x4
	nifInfo      = 0x10
	niifInfo     = 0x1
	niifUser     = 0x4
	wmLButtonUp  = 0x0202
	wmRButtonUp  = 0x0205
	wmCommand    = 0x0111
	wmDestroy    = 0x0002
	balloonClick = 0x0405 // NIN_BALLOONUSERCLICK
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	shell32          = windows.NewLazySystemDLL("shell32.dll")
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	registerClass    = user32.NewProc("RegisterClassExW")
	createWindow     = user32.NewProc("CreateWindowExW")
	defWindowProc    = user32.NewProc("DefWindowProcW")
	getMessage       = user32.NewProc("GetMessageW")
	translateMessage = user32.NewProc("TranslateMessage")
	dispatchMessage  = user32.NewProc("DispatchMessageW")
	postQuit         = user32.NewProc("PostQuitMessage")
	createPopupMenu  = user32.NewProc("CreatePopupMenu")
	appendMenu       = user32.NewProc("AppendMenuW")
	trackPopupMenu   = user32.NewProc("TrackPopupMenu")
	setForeground    = user32.NewProc("SetForegroundWindow")
	getCursorPos     = user32.NewProc("GetCursorPos")
	loadImage        = user32.NewProc("LoadImageW")
	loadIcon         = user32.NewProc("LoadIconW")
	destroyWindow    = user32.NewProc("DestroyWindow")
	shellNotifyIcon  = shell32.NewProc("Shell_NotifyIconW")
	getModuleHandle  = kernel32.NewProc("GetModuleHandleW")
	createMutex      = kernel32.NewProc("CreateMutexW")
)

type wndClassEx struct {
	Size, Style                        uint32
	WndProc                            uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background windows.Handle
	MenuName, ClassName                *uint16
	IconSm                             windows.Handle
}

type msg struct {
	Hwnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type notifyIconData struct {
	Size             uint32
	Wnd              windows.Handle
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             windows.Handle
	Tip              [128]uint16
	State, StateMask uint32
	Info             [256]uint16
	Version          uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUIDItem         windows.GUID
	BalloonIcon      windows.Handle
}

var (
	hwnd       windows.Handle
	icon       windows.Handle
	balloonMu  sync.Mutex
	balloonURL = appURL
)

func copyUTF16(dst []uint16, s string) {
	u, _ := windows.UTF16FromString(s)
	if len(u) > len(dst) {
		u = append(u[:len(dst)-1], 0)
	}
	copy(dst, u)
}

func iconData() notifyIconData {
	d := notifyIconData{Wnd: hwnd, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: trayMessage, Icon: icon}
	d.Size = uint32(unsafe.Sizeof(d))
	copyUTF16(d.Tip[:], "DJOneHub 蜂窝代理")
	return d
}

// notify shows a Windows notification from the tray icon; clicking it opens
// the window at page.
func notify(title, text, page string) {
	balloonMu.Lock()
	balloonURL = appURL + "?page=" + page
	balloonMu.Unlock()
	d := iconData()
	d.Flags |= nifInfo
	d.InfoFlags = niifUser
	copyUTF16(d.InfoTitle[:], title)
	copyUTF16(d.Info[:], text)
	shellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&d)))
}

// openWindow shows DJOneHub in an Edge app window (no browser toolbar, its
// own taskbar entry), falling back to the default browser.
func openWindow(url string) {
	profile := filepath.Join(os.Getenv("LOCALAPPDATA"), "DJOneHub", "window")
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA")} {
		if base == "" {
			continue
		}
		edge := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
		if _, err := os.Stat(edge); err != nil {
			continue
		}
		cmd := exec.Command(edge, "--app="+url, "--user-data-dir="+profile, "--window-size=1180,820", "--no-first-run", "--disable-features=Translate")
		if cmd.Start() == nil {
			return
		}
	}
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
}

func showMenu() {
	menu, _, _ := createPopupMenu.Call()
	open, _ := windows.UTF16PtrFromString("打开 DJOneHub")
	quit, _ := windows.UTF16PtrFromString("退出托盘（代理照常运行）")
	appendMenu.Call(menu, 0, menuOpen, uintptr(unsafe.Pointer(open)))
	appendMenu.Call(menu, 0x800, 0, 0) // separator
	appendMenu.Call(menu, 0, menuQuit, uintptr(unsafe.Pointer(quit)))
	var pt struct{ X, Y int32 }
	getCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	setForeground.Call(uintptr(hwnd))
	trackPopupMenu.Call(menu, 0, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)
}

func wndProc(h windows.Handle, m uint32, w, l uintptr) uintptr {
	switch m {
	case trayMessage:
		switch uint32(l) & 0xffff {
		case wmLButtonUp:
			openWindow(appURL)
		case wmRButtonUp:
			showMenu()
		case balloonClick:
			balloonMu.Lock()
			url := balloonURL
			balloonMu.Unlock()
			openWindow(url)
		}
		return 0
	case wmCommand:
		switch w & 0xffff {
		case menuOpen:
			openWindow(appURL)
		case menuQuit:
			d := iconData()
			shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&d)))
			destroyWindow.Call(uintptr(h))
		}
		return 0
	case wmDestroy:
		postQuit.Call(0)
		return 0
	}
	r, _, _ := defWindowProc.Call(uintptr(h), uintptr(m), w, l)
	return r
}

// ---- notifications --------------------------------------------------------

type smsMessage struct {
	Sender     string    `json:"sender"`
	Content    string    `json:"content"`
	Code       string    `json:"code"`
	Timestamp  time.Time `json:"timestamp"`
	ModuleName string    `json:"module_name"`
}

type callStatus struct {
	Active *struct {
		ID         string `json:"id"`
		State      string `json:"state"`
		Number     string `json:"number"`
		ModuleName string `json:"module_name"`
	} `json:"active"`
}

func getJSON(path string, into any) error {
	client := http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get("http://127.0.0.1:7575/" + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func watch() {
	var newest time.Time
	seenCall := ""
	offlineSince := time.Time{}
	warned := false
	for {
		var messages []smsMessage
		if err := getJSON("api/sms", &messages); err == nil {
			offlineSince, warned = time.Time{}, false
			latest := newest
			for _, m := range messages {
				if !newest.IsZero() && m.Timestamp.After(newest) {
					title := "新短信 · " + m.Sender
					if m.Code != "" {
						title = m.Code + " · " + m.Sender
					}
					if m.ModuleName != "" {
						title += "（" + m.ModuleName + "）"
					}
					notify(title, strings.TrimSpace(m.Content), "messages")
				}
				if m.Timestamp.After(latest) {
					latest = m.Timestamp
				}
			}
			if newest.IsZero() && latest.IsZero() {
				latest = time.Now()
			}
			newest = latest
		} else {
			if offlineSince.IsZero() {
				offlineSince = time.Now()
			}
			if !warned && time.Since(offlineSince) > 90*time.Second {
				warned = true
				notify("DJOneHub 后台没有运行", "代理和短信暂时不可用。可以在“服务”里启动“DJOneHub 蜂窝代理”，或重新安装 DJOneHub。", "proxy")
			}
		}
		var calls callStatus
		if getJSON("api/calls/status", &calls) == nil && calls.Active != nil {
			c := calls.Active
			if (c.State == "incoming" || c.State == "waiting") && c.ID != seenCall {
				seenCall = c.ID
				text := c.Number
				if c.ModuleName != "" {
					text += " · " + c.ModuleName
				}
				notify("来电", text, "recents")
			}
		}
		time.Sleep(4 * time.Second)
	}
}

func main() {
	runtime.LockOSThread()
	name, _ := windows.UTF16PtrFromString("Local\\DJOneHubTray")
	createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		// Already in the tray: a second start just opens the window.
		openWindow(appURL)
		return
	}
	instance, _, _ := getModuleHandle.Call(0)
	exe, _ := os.Executable()
	iconPath, _ := windows.UTF16PtrFromString(filepath.Join(filepath.Dir(exe), "DJOneHub.ico"))
	h, _, _ := loadImage.Call(0, uintptr(unsafe.Pointer(iconPath)), 1 /* IMAGE_ICON */, 0, 0, 0x10|0x40 /* LR_LOADFROMFILE|LR_DEFAULTSIZE */)
	if h == 0 {
		h, _, _ = loadIcon.Call(0, 32512) // IDI_APPLICATION
	}
	icon = windows.Handle(h)
	className, _ := windows.UTF16PtrFromString("DJOneHubTray")
	wc := wndClassEx{WndProc: windows.NewCallback(wndProc), Instance: windows.Handle(instance), ClassName: className}
	wc.Size = uint32(unsafe.Sizeof(wc))
	registerClass.Call(uintptr(unsafe.Pointer(&wc)))
	w, _, _ := createWindow.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	hwnd = windows.Handle(w)
	d := iconData()
	shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&d)))
	if len(os.Args) < 2 || os.Args[1] != "--background" {
		openWindow(appURL)
	}
	go watch()
	var m msg
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}
