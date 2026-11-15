//go:build windows

// DJOneHub.exe is the Windows desktop side, like the macOS menu bar app: a
// tray icon that opens the DJOneHub window and shows a notification for each
// new SMS and incoming call. The modules and proxies run in the DJOneHub
// service, so closing the window or quitting the tray changes nothing there.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	loadIconW        = user32.NewProc("LoadIconW")
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

// ---- notifications, calls and settings -----------------------------------

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
		ModuleID   string `json:"module_id"`
		ModuleName string `json:"module_name"`
	} `json:"active"`
	Host *struct {
		Enabled   bool `json:"enabled"`
		Muted     bool `json:"muted"`
		Recording bool `json:"recording"`
	} `json:"host"`
}

type desktopSettings struct {
	Notifications bool      `json:"notifications"`
	ShowContents  bool      `json:"show_contents"`
	Ringtone      string    `json:"ringtone"`
	QuitAt        time.Time `json:"quit_at"`
}

type hostConfig struct {
	RouteReady  bool   `json:"route_ready"`
	RouteError  string `json:"route_error"`
	USBInstance string `json:"usb_instance"`
}

var apiClient = http.Client{Timeout: 8 * time.Second}

func request(method, path string, body any, into any) error {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://127.0.0.1:7575/"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func getJSON(path string, into any) error { return request("GET", path, nil, into) }

var started = time.Now()

func quit() {
	stopRingtone()
	audio.stop()
	closeWindow()
	d := iconData()
	shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&d)))
	os.Exit(0)
}

func watch() {
	var newest time.Time
	seenCall := ""
	offlineSince := time.Time{}
	warned := false
	settings := desktopSettings{Notifications: true, Ringtone: "Ring01"}
	lastRegister := time.Time{}
	for {
		if err := getJSON("api/desktop/settings", &settings); err == nil && settings.QuitAt.After(started) {
			quit()
		}
		if time.Since(lastRegister) > 30*time.Second {
			if request("POST", "api/calls/audio/host/register", map[string]bool{"enabled": true}, nil) == nil {
				lastRegister = time.Now()
			}
		}
		var messages []smsMessage
		if err := getJSON("api/sms", &messages); err == nil {
			offlineSince, warned = time.Time{}, false
			latest := newest
			for _, m := range messages {
				if !newest.IsZero() && m.Timestamp.After(newest) && settings.Notifications {
					title, text := "新短信 · "+m.Sender, "收到一条新短信"
					if settings.ShowContents {
						text = strings.TrimSpace(m.Content)
						if m.Code != "" {
							title = m.Code + " · " + m.Sender
						}
					}
					if m.ModuleName != "" {
						title += "（" + m.ModuleName + "）"
					}
					notify(title, text, "messages")
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
				notify("DJOneHub 后台没有运行", "代理和短信暂时不可用。双击桌面上的 DJOneHub 可以重新启动后台。", "proxy")
			}
		}
		interval := 4 * time.Second
		var calls callStatus
		if getJSON("api/calls/status", &calls) == nil && calls.Active != nil {
			c := calls.Active
			ringingNow := c.State == "incoming" || c.State == "waiting"
			if ringingNow {
				startRingtone(settings.Ringtone)
				if c.ID != seenCall {
					seenCall = c.ID
					text := c.Number
					if c.ModuleName != "" {
						text += " · " + c.ModuleName
					}
					if settings.Notifications {
						notify("来电", text, "recents")
					}
					openWindow(appURL + "?page=recents")
				}
			} else {
				stopRingtone()
			}
			if c.State == "active" {
				audio.ensure(c.ID, calls.Host)
				interval = time.Second
			} else {
				audio.stop()
			}
		} else {
			stopRingtone()
			audio.stop()
		}
		time.Sleep(interval)
	}
}

// ---- call audio ------------------------------------------------------------

type audioHost struct {
	mu      sync.Mutex
	callID  string
	bridge  *callBridge
	err     string
	detail  string
	tries   int
	nextTry time.Time
}

var audio audioHost

func (a *audioHost) report() {
	a.mu.Lock()
	state := map[string]any{"running": a.bridge != nil, "error": a.err, "detail": a.detail}
	a.mu.Unlock()
	request("POST", "api/desktop/audio", state, nil)
}

// ensure keeps this PC's microphone and speaker on the active call.
func (a *audioHost) ensure(callID string, host *struct {
	Enabled   bool `json:"enabled"`
	Muted     bool `json:"muted"`
	Recording bool `json:"recording"`
}) {
	a.mu.Lock()
	if a.callID != callID {
		a.mu.Unlock()
		a.stop()
		a.mu.Lock()
		a.callID, a.err, a.detail, a.tries, a.nextTry = callID, "", "正在准备模块语音通道…", 0, time.Time{}
	}
	b := a.bridge
	due := b == nil && time.Now().After(a.nextTry) && a.tries < 6
	a.mu.Unlock()
	if b != nil {
		if host != nil {
			b.muted.Store(host.Muted)
			if host.Recording != b.recording() {
				b.setRecording(host.Recording)
			}
		}
		a.report()
		return
	}
	if due {
		a.start()
	}
	a.report()
}

func (a *audioHost) start() {
	var cfg hostConfig
	err := getJSON("api/calls/audio/host/config", &cfg)
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case err != nil:
		a.detail = "正在准备模块语音通道…"
	case cfg.RouteError != "":
		a.tries++
		a.nextTry = time.Now().Add(5 * time.Second)
		a.err = "模块语音通道准备失败：" + cfg.RouteError
		if strings.Contains(cfg.RouteError, "ADB") {
			a.err = "这个模块还没开启通话支持：到 设置 › 通话支持 点“启用通话支持”。"
		}
	case !cfg.RouteReady:
		a.detail = "正在准备模块语音通道…"
	default:
		b, err := startCallBridge(cfg.USBInstance)
		if err != nil {
			a.tries++
			a.nextTry = time.Now().Add(3 * time.Second)
			a.err = err.Error()
			return
		}
		a.bridge, a.err, a.detail = b, "", ""
	}
}

func (a *audioHost) stop() {
	a.mu.Lock()
	b := a.bridge
	a.bridge, a.callID = nil, ""
	a.mu.Unlock()
	if b != nil {
		b.close()
		request("POST", "api/desktop/audio", map[string]any{"running": false}, nil)
	}
}

// ---- service ---------------------------------------------------------------

// ensureService starts the DJOneHub service when it was stopped (完全退出),
// asking for administrator approval like any other system change.
func ensureService() {
	if getJSON("api/platform", &struct{}{}) == nil {
		return
	}
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return
	}
	defer windows.CloseServiceHandle(scm)
	s, err := windows.OpenService(scm, utf16("DJOneHub"), windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return
	}
	defer windows.CloseServiceHandle(s)
	var st windows.SERVICE_STATUS
	if windows.QueryServiceStatus(s, &st) != nil || st.CurrentState != windows.SERVICE_STOPPED {
		return
	}
	exe, _ := os.Executable()
	service := filepath.Join(filepath.Dir(exe), "djonehub-service.exe")
	windows.ShellExecute(0, utf16("runas"), utf16(service), utf16("--start-service"), nil, 0)
	for i := 0; i < 40 && getJSON("api/platform", &struct{}{}) != nil; i++ {
		time.Sleep(500 * time.Millisecond)
	}
}

func loadIcon() {
	exe, _ := os.Executable()
	iconPath, _ := windows.UTF16PtrFromString(filepath.Join(filepath.Dir(exe), "DJOneHub.ico"))
	h, _, _ := loadImage.Call(0, uintptr(unsafe.Pointer(iconPath)), 1 /* IMAGE_ICON */, 0, 0, 0x10|0x40 /* LR_LOADFROMFILE|LR_DEFAULTSIZE */)
	if h == 0 {
		h, _, _ = loadIconW.Call(0, 32512) // IDI_APPLICATION
	}
	icon = windows.Handle(h)
}

func main() {
	runtime.LockOSThread()
	loadIcon()
	if len(os.Args) >= 2 && os.Args[1] == "--window" {
		url := appURL
		if len(os.Args) >= 3 && strings.HasPrefix(os.Args[2], appURL) {
			url = os.Args[2]
		}
		runWindow(url)
		return
	}
	background := len(os.Args) >= 2 && os.Args[1] == "--background"
	name, _ := windows.UTF16PtrFromString("Local\\DJOneHubTray")
	createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		// Already in the tray: a second start just opens the window.
		openWindow(appURL)
		return
	}
	instance, _, _ := getModuleHandle.Call(0)
	className, _ := windows.UTF16PtrFromString("DJOneHubTray")
	wc := wndClassEx{WndProc: windows.NewCallback(wndProc), Instance: windows.Handle(instance), ClassName: className}
	wc.Size = uint32(unsafe.Sizeof(wc))
	registerClass.Call(uintptr(unsafe.Pointer(&wc)))
	w, _, _ := createWindow.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	hwnd = windows.Handle(w)
	d := iconData()
	shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&d)))
	if !background {
		ensureService()
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
