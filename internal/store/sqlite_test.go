package store

import (
	"path/filepath"
	"testing"
)

func TestOpenSQLiteCreatesDefaultSettings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "signin.db")
	st, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer st.Close()
	settings, err := st.GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}
	if settings.Web.Username == "" {
		t.Fatal("default web username is empty")
	}
}
