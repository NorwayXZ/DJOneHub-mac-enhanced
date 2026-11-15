package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// The macOS app reads the system address book. Windows has none, so its app
// keeps contacts here, in one file shared by every module.

type storedContact struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Phones []string `json:"phones"`
	Note   string   `json:"note,omitempty"`
}

func contactsPath() string {
	dir := fleetStateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "contacts.json")
}

func loadContacts() []storedContact {
	contacts := []storedContact{}
	if b, err := os.ReadFile(contactsPath()); err == nil {
		_ = json.Unmarshal(b, &contacts)
	}
	return contacts
}

func contactsAPI(w http.ResponseWriter, r *http.Request) {
	path := contactsPath()
	if path == "" {
		writeError(w, 500, "无法确定通讯录保存位置")
		return
	}
	if r.Method == http.MethodPut {
		var contacts []storedContact
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&contacts); err != nil {
			writeError(w, 400, "invalid request")
			return
		}
		clean := make([]storedContact, 0, len(contacts))
		for _, c := range contacts {
			c.Name = strings.TrimSpace(c.Name)
			phones := c.Phones[:0]
			for _, p := range c.Phones {
				if p = strings.TrimSpace(p); p != "" {
					phones = append(phones, p)
				}
			}
			c.Phones = phones
			if c.Name == "" && len(c.Phones) == 0 {
				continue
			}
			clean = append(clean, c)
		}
		err := withSharedFileLock(path, func() error {
			b, _ := json.MarshalIndent(clean, "", "  ")
			return os.WriteFile(path, b, 0600)
		})
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, loadContacts())
}
