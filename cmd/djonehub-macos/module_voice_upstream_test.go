//go:build (darwin && cgo) || windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestUpstreamVoiceManifestIsComplete(t *testing.T) {
	manifest, err := loadVoiceManifest()
	if err != nil {
		t.Fatalf("loadVoiceManifest() error = %v", err)
	}
	if manifest.KernelRelease != "3.18.44" || manifest.Helper == "" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if len(manifest.Files) != len(upstreamVoiceFiles) || len(manifest.Modules) != 2 {
		t.Fatalf("manifest files/modules = %d/%d", len(manifest.Files), len(manifest.Modules))
	}
	for _, file := range upstreamVoiceFiles {
		if len(file.SHA256) != 64 {
			t.Fatalf("%s has invalid SHA-256", file.Name)
		}
		if file.Mode == 0 {
			t.Fatalf("%s has no file mode", file.Name)
		}
	}
}

func TestMatchesSHA256(t *testing.T) {
	data := []byte("DJOneHub upstream runtime validation")
	if !matchesSHA256(data, "16c24e97c3b040c2397d38e6bbb89be3fd67cb6b45c84236db741ea4fd452008") {
		t.Fatal("known SHA-256 did not match")
	}
	if matchesSHA256(data, "0000000000000000000000000000000000000000000000000000000000000000") {
		t.Fatal("incorrect SHA-256 matched")
	}
}

func TestVoiceProvisionRequiresConfirmation(t *testing.T) {
	instance := &app{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/voice/provision", bytes.NewBufferString(`{"confirm":false}`))
	instance.voiceProvisionAPI(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("voiceProvisionAPI() status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestVoiceStatusNeverClaimsBundledRuntime(t *testing.T) {
	status := (&app{}).voiceStatus()
	if status["runtime_included"] != false {
		t.Fatalf("runtime_included = %#v, want false", status["runtime_included"])
	}
	if status["runtime_source"] != upstreamVoiceRuntimeSource {
		t.Fatalf("runtime_source = %#v, want %q", status["runtime_source"], upstreamVoiceRuntimeSource)
	}
}

func TestVoiceRuntimeDownloadFallsBackAfterHTTP500(t *testing.T) {
	payload := []byte("verified runtime fallback payload")
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporary upstream failure", http.StatusInternalServerError)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github.raw" {
			t.Fatalf("Accept = %q", got)
		}
		_, _ = w.Write(payload)
	}))
	defer second.Close()

	dir := t.TempDir()
	sum := sha256.Sum256(payload)
	file := upstreamVoiceFile{Name: "runtime.bin", Mode: 0o700, SHA256: fmt.Sprintf("%x", sum)}
	err := downloadVerifiedVoiceFileFromSources(context.Background(), first.Client(), dir, file, []upstreamVoiceDownloadSource{
		{Name: "primary", URL: first.URL},
		{Name: "fallback", URL: second.URL},
	})
	if err != nil {
		t.Fatalf("download fallback error = %v", err)
	}
	installed, err := os.ReadFile(dir + "/runtime.bin")
	if err != nil || !bytes.Equal(installed, payload) {
		t.Fatalf("installed runtime = %q, err=%v", installed, err)
	}
}
