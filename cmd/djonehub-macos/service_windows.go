//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// DJOneHub runs as a Windows service so the modules and proxies work from
// boot, without anyone signed in, with the rights to bind module drivers.

const (
	serviceName        = "DJOneHub"
	serviceDisplayName = "DJOneHub 蜂窝代理"
)

// isServiceInvocation reports whether the service manager started us. It is
// checked before anything writes state, because a service keeps its state in
// ProgramData instead of a user profile.
func isServiceInvocation() bool {
	for _, arg := range os.Args[1:] {
		if arg == "--service" || arg == "-service" {
			return true
		}
	}
	return false
}

// prepareServiceEnvironment points the profile folders at ProgramData, so
// os.UserConfigDir and the log folder resolve there for the service and the
// module workers it starts.
func prepareServiceEnvironment() {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	_ = os.Setenv("APPDATA", base)
	_ = os.Setenv("LOCALAPPDATA", base)
}

type djonehubService struct{ run func() }

func (s djonehubService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	done := make(chan struct{})
	go func() { defer close(done); s.run() }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case <-done:
			return false, 0
		case r := <-requests:
			switch r.Cmd {
			case svc.Interrogate:
				status <- r.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 20000}
				requestServiceStop()
				select {
				case <-done:
				case <-time.After(18 * time.Second):
					log.Printf("service stop: shutdown still running, exiting")
				}
				return false, 0
			}
		}
	}
}

// runAsService hands run to the service manager when started as a service;
// it reports false when this is a normal start.
func runAsService(run func()) bool {
	if !isServiceInvocation() {
		return false
	}
	relaunchTrayAfterUpdate()
	if err := svc.Run(serviceName, djonehubService{run: run}); err != nil {
		log.Printf("service: %v", err)
	}
	return true
}

// installService registers (or updates) and starts the DJOneHub service.
func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("需要管理员权限: %w", err)
	}
	defer m.Disconnect()
	config := mgr.Config{
		DisplayName:      serviceDisplayName,
		Description:      "管理 4G 模块、短信和蜂窝代理。开机自动运行，无需登录。",
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: false,
	}
	// An existing registration is removed and created again, so a moved or
	// renamed binary and a stuck earlier start can never survive an upgrade.
	if old, err := m.OpenService(serviceName); err == nil {
		removeService(old)
		for i := 0; i < 50; i++ {
			probe, err := m.OpenService(serviceName)
			if err != nil {
				break
			}
			probe.Close()
			time.Sleep(200 * time.Millisecond)
		}
	}
	s, err := m.CreateService(serviceName, exe, config, "--service")
	if err != nil {
		return err
	}
	defer s.Close()
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400)
	return s.Start()
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("需要管理员权限: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return nil
	}
	removeService(s)
	return nil
}

// removeService stops the service, ending its process if it does not stop
// (a start that never completed cannot be stopped), and deletes it.
func removeService(s *mgr.Service) {
	defer s.Close()
	_, _ = s.Control(svc.Stop)
	waitServiceStopped(s)
	if st, err := s.Query(); err == nil && st.State != svc.Stopped && st.ProcessId != 0 {
		if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, st.ProcessId); err == nil {
			_ = windows.TerminateProcess(h, 1)
			windows.CloseHandle(h)
		}
		waitServiceStopped(s)
	}
	_ = s.Delete()
}

func waitServiceStopped(s *mgr.Service) {
	for i := 0; i < 50; i++ {
		st, err := s.Query()
		if err != nil || st.State == svc.Stopped {
			return
		}
		time.Sleep(400 * time.Millisecond)
	}
}

// handleServiceCommand runs --install-service / --uninstall-service.
func handleServiceCommand() bool {
	if len(os.Args) < 2 {
		return false
	}
	var err error
	switch strings.TrimLeft(os.Args[1], "-") {
	case "install-service":
		err = installService()
	case "uninstall-service":
		err = uninstallService()
	case "start-service":
		err = startService()
	default:
		return false
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return true
}

// startService starts the installed service (the tray runs this elevated when
// DJOneHub is opened after 完全退出).
func startService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("需要管理员权限: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Start()
}
