//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// One-click update, like the macOS app's: download the installer from this
// repository's GitHub release, check its SHA-256, run it silently, and open
// the DJOneHub window again once the new version is running.

const updateRepo = "NorwayXZ/DJOneHub-mac-enhanced"

var updateTagPattern = regexp.MustCompile(`^v[0-9A-Za-z.\-]+$`)

type windowsUpdateState struct {
	State    string  `json:"state"` // idle, downloading, installing, failed
	Tag      string  `json:"tag,omitempty"`
	Progress float64 `json:"progress"`
	Error    string  `json:"error,omitempty"`
}

var windowsUpdate struct {
	sync.Mutex
	s windowsUpdateState
}

func setWindowsUpdate(fn func(*windowsUpdateState)) {
	windowsUpdate.Lock()
	fn(&windowsUpdate.s)
	windowsUpdate.Unlock()
}

func windowsUpdateStatusAPI(w http.ResponseWriter, _ *http.Request) {
	windowsUpdate.Lock()
	s := windowsUpdate.s
	windowsUpdate.Unlock()
	if s.State == "" {
		s.State = "idle"
	}
	writeJSON(w, 200, s)
}

func windowsUpdateStartAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tag string `json:"tag"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !updateTagPattern.MatchString(body.Tag) {
		writeError(w, 400, "版本号无效")
		return
	}
	windowsUpdate.Lock()
	busy := windowsUpdate.s.State == "downloading" || windowsUpdate.s.State == "installing"
	if !busy {
		windowsUpdate.s = windowsUpdateState{State: "downloading", Tag: body.Tag}
	}
	windowsUpdate.Unlock()
	if busy {
		writeError(w, 409, "正在更新")
		return
	}
	go func() {
		if err := runWindowsUpdate(body.Tag); err != nil {
			log.Printf("update %s failed: %v", body.Tag, err)
			setWindowsUpdate(func(s *windowsUpdateState) { s.State, s.Error = "failed", err.Error() })
		}
	}()
	writeJSON(w, 202, map[string]string{"state": "downloading"})
}

type releaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

func runWindowsUpdate(tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	client := &http.Client{}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+updateRepo+"/releases/tags/"+tag, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 GitHub：%w", err)
	}
	var release struct {
		Assets []releaseAsset `json:"assets"`
	}
	err = json.NewDecoder(resp.Body).Decode(&release)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 {
		return fmt.Errorf("读取 %s 发布信息失败（HTTP %d）", tag, resp.StatusCode)
	}
	name := "DJOneHub-Windows-Setup-" + tag + ".exe"
	var installer, checksum *releaseAsset
	for i := range release.Assets {
		switch release.Assets[i].Name {
		case name:
			installer = &release.Assets[i]
		case name + ".sha256":
			checksum = &release.Assets[i]
		}
	}
	if installer == nil || !strings.HasPrefix(installer.URL, "https://github.com/"+updateRepo+"/") {
		return errors.New("这个版本没有 Windows 安装包")
	}
	want := strings.TrimPrefix(installer.Digest, "sha256:")
	if len(want) != 64 && checksum != nil {
		if b, err := httpGetSmall(ctx, client, checksum.URL); err == nil {
			want = strings.ToLower(strings.Fields(string(b) + " ")[0])
		}
	}
	if len(want) != 64 {
		return errors.New("找不到安装包的 SHA-256，已停止更新")
	}

	dir := filepath.Join(fleetStateDir(), "updates")
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	req, _ = http.NewRequestWithContext(ctx, "GET", installer.URL, nil)
	resp, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("下载失败（HTTP %d）", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	total := max(installer.Size, resp.ContentLength)
	var done int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			f.Write(buf[:n])
			hash.Write(buf[:n])
			done += int64(n)
			if total > 0 {
				p := float64(done) / float64(total)
				setWindowsUpdate(func(s *windowsUpdateState) { s.Progress = p })
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return fmt.Errorf("下载中断：%w", rerr)
		}
	}
	f.Close()
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, want) {
		os.Remove(path)
		return errors.New("安装包校验失败（SHA-256 不一致），已删除")
	}

	setWindowsUpdate(func(s *windowsUpdateState) { s.State, s.Progress = "installing", 1 })
	// The new service reopens the window for whoever is signed in.
	_ = os.WriteFile(filepath.Join(fleetStateDir(), "relaunch-tray"), []byte(tag), 0644)
	cmd := exec.Command(path, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-", "/LOG="+filepath.Join(dir, "install.log"))
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB}
	if err := cmd.Start(); err != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
		if err := cmd.Start(); err != nil {
			os.Remove(filepath.Join(fleetStateDir(), "relaunch-tray"))
			return fmt.Errorf("无法启动安装程序：%w", err)
		}
	}
	log.Printf("update %s: installer started (pid %d)", tag, cmd.Process.Pid)
	return nil
}

func httpGetSmall(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 4096))
}

// relaunchTrayAfterUpdate opens DJOneHub (tray and window) in every signed-in
// session after a one-click update replaced it.
func relaunchTrayAfterUpdate() {
	marker := filepath.Join(fleetStateDir(), "relaunch-tray")
	if _, err := os.Stat(marker); err != nil {
		return
	}
	os.Remove(marker)
	exe, err := os.Executable()
	if err != nil {
		return
	}
	tray := filepath.Join(filepath.Dir(exe), "DJOneHub.exe")
	go func() {
		// Let the API come up first, so the window opens on a live page.
		time.Sleep(4 * time.Second)
		launchInUserSessions(tray, "")
	}()
}

func launchInUserSessions(exe, args string) {
	var sessions *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &sessions, &count); err != nil {
		log.Printf("relaunch tray: %v", err)
		return
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(sessions)))
	for _, s := range unsafe.Slice(sessions, count) {
		if s.State != windows.WTSActive {
			continue
		}
		var token windows.Token
		if err := windows.WTSQueryUserToken(s.SessionID, &token); err != nil {
			continue
		}
		var env *uint16
		_ = windows.CreateEnvironmentBlock(&env, token, false)
		cmdline, _ := windows.UTF16PtrFromString(`"` + exe + `" ` + args)
		desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
		dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
		si := windows.StartupInfo{Desktop: desktop}
		si.Cb = uint32(unsafe.Sizeof(si))
		var pi windows.ProcessInformation
		err := windows.CreateProcessAsUser(token, nil, cmdline, nil, nil, false, windows.CREATE_UNICODE_ENVIRONMENT, env, dir, &si, &pi)
		if env != nil {
			windows.DestroyEnvironmentBlock(env)
		}
		token.Close()
		if err != nil {
			log.Printf("relaunch tray in session %d: %v", s.SessionID, err)
			continue
		}
		windows.CloseHandle(pi.Thread)
		windows.CloseHandle(pi.Process)
		log.Printf("DJOneHub reopened in session %d", s.SessionID)
	}
}
