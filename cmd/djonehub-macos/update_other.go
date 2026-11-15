//go:build !windows

package main

import "net/http"

func windowsUpdateStatusAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{"state": "idle"})
}

func windowsUpdateStartAPI(w http.ResponseWriter, _ *http.Request) {
	writeError(w, 400, "仅用于 Windows 版")
}
