// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jamesplotts/layforge/master/internal/store"
)

// TestOpenSQLiteEventStore_ConcurrentOpenOnSameFile is the regression for
// a restart crash: the replacement process's schema init raced the
// outgoing process's still-open handle and died with SQLITE_BUSY. With
// busy_timeout applied via the DSN on every connection, a second open of
// the same file while the first is still live must succeed.
func TestOpenSQLiteEventStore_ConcurrentOpenOnSameFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")

	first, err := store.OpenSQLiteEventStore(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	defer first.Close()

	// Give the first store an active writer so it genuinely holds locks.
	if err := first.AppendEvent(context.Background(), testEvent("c", "m1")); err != nil {
		t.Fatalf("first append: %v", err)
	}

	second, err := store.OpenSQLiteEventStore(path)
	if err != nil {
		t.Fatalf("second open while first is live: %v — the restart race is back", err)
	}
	defer second.Close()

	if err := second.AppendEvent(context.Background(), testEvent("c", "m2")); err != nil {
		t.Fatalf("second append: %v", err)
	}
}

// TestOpenSQLiteEventStore_FilePathAcceptsExplicitFileURI confirms a
// caller can still pass a "file:"-prefixed DSN (fileDSNWithPragmas must
// not double-prefix it).
func TestOpenSQLiteEventStore_FilePathAcceptsExplicitFileURI(t *testing.T) {
	path := "file:" + filepath.Join(t.TempDir(), "uri.db")
	s, err := store.OpenSQLiteEventStore(path)
	if err != nil {
		t.Fatalf("open with file: URI: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
