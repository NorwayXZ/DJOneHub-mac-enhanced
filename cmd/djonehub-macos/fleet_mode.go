package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func modeArguments(args []string, enabled bool) []string {
	result := []string{}
	for _, arg := range args {
		if arg == "--multi-module" || arg == "-multi-module" || strings.HasPrefix(arg, "--multi-module=") || strings.HasPrefix(arg, "-multi-module=") {
			continue
		}
		result = append(result, arg)
	}
	if enabled {
		result = append(result, "--multi-module")
	}
	return result
}
func modeLaunchAgent() (string, map[string]any, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(home, "Library", "LaunchAgents", "com.jamie.djonehub.plist")
	b, err := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-", path).Output()
	if err != nil {
		return "", nil, err
	}
	var plist map[string]any
	if err = json.Unmarshal(b, &plist); err != nil {
		return "", nil, err
	}
	args, ok := plist["ProgramArguments"].([]any)
	if !ok || len(args) == 0 {
		return "", nil, errors.New("invalid installed service")
	}
	target, ok := args[0].(string)
	if !ok {
		return "", nil, errors.New("invalid service executable")
	}
	executable, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", nil, err
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil || target != executable {
		return "", nil, errors.New("only the installed DJOneHub service can switch modes")
	}
	return path, plist, nil
}
func persistModuleMode(enabled bool) error {
	path, plist, err := modeLaunchAgent()
	if err != nil {
		return err
	}
	existing := plist["ProgramArguments"].([]any)
	args := []string{}
	for _, v := range existing {
		s, ok := v.(string)
		if !ok {
			return errors.New("invalid service arguments")
		}
		args = append(args, s)
	}
	plist["ProgramArguments"] = modeArguments(args, enabled)
	b, err := json.Marshal(plist)
	if err != nil {
		return err
	}
	command := exec.Command("/usr/bin/plutil", "-convert", "xml1", "-o", "-", "-")
	command.Stdin = bytes.NewReader(b)
	xml, err := command.Output()
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".djonehub-agent-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(xml); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
func moduleModeStatus(w http.ResponseWriter, r *http.Request) {
	_, _, err := modeLaunchAgent()
	writeJSON(w, 200, map[string]any{"enabled": coordinatorOnly, "switch_available": err == nil && scopedUSBSession == 0})
}
func requestedModuleMode(w http.ResponseWriter, r *http.Request) (bool, bool) {
	var body struct {
		Enabled bool `json:"enabled"`
		Confirm bool `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return false, false
	}
	if !body.Confirm {
		writeError(w, 400, "切换模式将重启后台，需要确认")
		return false, false
	}
	if scopedUSBSession != 0 {
		writeError(w, 403, "mode changes belong to the supervisor")
		return false, false
	}
	if _, _, err := modeLaunchAgent(); err != nil {
		writeError(w, 409, err.Error())
		return false, false
	}
	return body.Enabled, true
}
func finishModuleModeChange(w http.ResponseWriter, enabled bool) {
	if err := persistModuleMode(enabled); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	home, _ := os.UserHomeDir()
	uid := os.Getuid()
	target := "gui/" + strconv.Itoa(uid)
	agent := filepath.Join(home, "Library/LaunchAgents/com.jamie.djonehub.plist")
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
	script := "sleep 1; /bin/launchctl bootout " + target + "/com.jamie.djonehub >/dev/null 2>&1; sleep 1; /bin/launchctl bootstrap " + target + " " + quote(agent)
	command := exec.Command("/bin/launchctl", "submit", "-l", fmt.Sprintf("com.jamie.djonehub-mode-switch.%d", os.Getpid()), "--", "/bin/sh", "-c", script)
	if err := command.Run(); err != nil {
		writeError(w, 500, "模式配置已保存，后台重启未成功，请重新启动服务")
		return
	}
	writeJSON(w, 200, map[string]bool{"restarting": true})
}
func (a *app) moduleModeChange(w http.ResponseWriter, r *http.Request) {
	enabled, ok := requestedModuleMode(w, r)
	if !ok {
		return
	}
	if err := a.beginUpdateLease(); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	finishModuleModeChange(w, enabled)
}
