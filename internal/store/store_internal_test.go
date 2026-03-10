package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tack/internal/issues"
)

func TestOpenConfiguresSQLitePragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tack", "issues.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		err := s.Close()
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	var foreignKeys int

	err = s.db.QueryRowContext(context.Background(), `PRAGMA foreign_keys;`).Scan(&foreignKeys)
	if err != nil {
		t.Fatalf("query foreign_keys pragma: %v", err)
	}

	if foreignKeys != 1 {
		t.Fatalf("expected foreign_keys pragma to be enabled, got %d", foreignKeys)
	}

	var journalMode string

	err = s.db.QueryRowContext(context.Background(), `PRAGMA journal_mode;`).Scan(&journalMode)
	if err != nil {
		t.Fatalf("query journal_mode pragma: %v", err)
	}

	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("expected journal_mode WAL, got %q", journalMode)
	}

	var busyTimeout int

	err = s.db.QueryRowContext(context.Background(), `PRAGMA busy_timeout;`).Scan(&busyTimeout)
	if err != nil {
		t.Fatalf("query busy_timeout pragma: %v", err)
	}

	if busyTimeout != sqliteBusyTimeout {
		t.Fatalf("expected busy_timeout %dms, got %d", sqliteBusyTimeout, busyTimeout)
	}
}

func TestConcurrentOpenersWaitForLockAndReadSuccessfully(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), ".tack", "issues.db")

	seed, err := Open(path)
	if err != nil {
		t.Fatalf("Open seed store: %v", err)
	}

	issue, err := seed.CreateIssue(ctx, CreateIssueInput{
		Title:       "ready",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	err = seed.Close()
	if err != nil {
		t.Fatalf("Close seed store: %v", err)
	}

	lockDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open lockDB: %v", err)
	}

	transactionOpen := true

	t.Cleanup(func() {
		if transactionOpen {
			_, err = lockDB.ExecContext(ctx, `ROLLBACK;`)
			if err != nil {
				t.Fatalf("ROLLBACK: %v", err)
			}
		}

		err = lockDB.Close()
		if err != nil {
			t.Fatalf("Close lockDB: %v", err)
		}
	})

	_, err = lockDB.ExecContext(ctx, `BEGIN IMMEDIATE;`)
	if err != nil {
		t.Fatalf("BEGIN IMMEDIATE: %v", err)
	}

	const readers = 4

	start := make(chan struct{})
	errCh := make(chan error, readers)

	for i := 0; i < readers; i++ {
		go func() {
			<-start

			errCh <- openAndReadIssue(ctx, path, issue.ID)
		}()
	}

	close(start)

	select {
	case err := <-errCh:
		t.Fatalf("open/read returned before lock release: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	_, err = lockDB.ExecContext(ctx, `COMMIT;`)
	if err != nil {
		t.Fatalf("COMMIT: %v", err)
	}

	transactionOpen = false

	for i := 0; i < readers; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("concurrent open/read failed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent readers")
		}
	}
}

func openAndReadIssue(ctx context.Context, path, issueID string) (err error) {
	s, err := Open(path)
	if err != nil {
		return err
	}

	defer func() {
		closeErr := s.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	ready, err := s.ReadyIssues(ctx, ListFilter{})
	if err != nil {
		return err
	}

	if len(ready) != 1 || ready[0].ID != issueID {
		return fmt.Errorf("unexpected ready issues: %#v", ready)
	}

	exported, err := s.Export(ctx)
	if err != nil {
		return err
	}

	if len(exported.Issues) != 1 || exported.Issues[0].ID != issueID {
		return fmt.Errorf("unexpected export issues: %#v", exported.Issues)
	}

	return nil
}
