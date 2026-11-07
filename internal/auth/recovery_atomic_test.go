package auth

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func recoveryTestDB(t *testing.T, codes []string) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	if err := New(database).EnsureOwner(context.Background()); err != nil {
		t.Fatal(err)
	}
	hashes := make([]string, len(codes))
	for i, code := range codes {
		hashes[i] = hashRecovery(normRecovery(code))
	}
	if _, err := database.Exec(`UPDATE users SET recovery_codes_hash=? WHERE id='owner'`, strings.Join(hashes, ",")); err != nil {
		t.Fatal(err)
	}
	return database
}

func consumeConcurrently(t *testing.T, database *sql.DB, codes []string) int32 {
	t.Helper()
	ctx := context.Background()
	// Queue the reads on the single production connection before releasing it.
	conn, err := database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before := database.Stats().WaitCount
	var successes atomic.Int32
	var wg sync.WaitGroup
	for _, code := range codes {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			// Separate Service instances must also share the single-use guarantee.
			if New(database).useRecoveryCode(ctx, code) {
				successes.Add(1)
			}
		}(code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for database.Stats().WaitCount-before < int64(len(codes)) {
		if time.Now().After(deadline) {
			t.Fatal("callers did not queue for the database connection")
		}
		time.Sleep(time.Millisecond)
	}
	conn.Close()
	wg.Wait()
	return successes.Load()
}

func TestRecoveryCodeConcurrentSingleUse(t *testing.T) {
	const code = "abcde23456"
	database := recoveryTestDB(t, []string{code})
	codes := make([]string, 32)
	for i := range codes {
		codes[i] = code
	}
	if got := consumeConcurrently(t, database, codes); got != 1 {
		t.Fatalf("one recovery code accepted %d times; want exactly 1", got)
	}
	if got := New(database).RecoveryCodesLeft(context.Background()); got != 0 {
		t.Fatalf("codes remaining = %d; want 0", got)
	}
}

func TestRecoveryCodeConcurrentDifferentCodes(t *testing.T) {
	codes := make([]string, 10)
	for i := range codes {
		codes[i] = fmt.Sprintf("abcde%05d", i)
	}
	database := recoveryTestDB(t, codes)
	if got := consumeConcurrently(t, database, codes); got != int32(len(codes)) {
		t.Fatalf("accepted %d different codes; want %d", got, len(codes))
	}
	s := New(database)
	for _, code := range codes {
		if s.useRecoveryCode(context.Background(), code) {
			t.Fatalf("consumed code %q was restored", code)
		}
	}
	if got := s.RecoveryCodesLeft(context.Background()); got != 0 {
		t.Fatalf("codes remaining = %d; want 0", got)
	}
}

func TestRecoveryCodeNormalizationAndFailures(t *testing.T) {
	database := recoveryTestDB(t, []string{"abcde23456", "fghjk23456"})
	s := New(database)
	ctx := context.Background()
	for _, invalid := range []string{"", "short", "zzzzz99999"} {
		if s.useRecoveryCode(ctx, invalid) {
			t.Fatalf("accepted invalid code %q", invalid)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if s.useRecoveryCode(cancelled, "abcde23456") {
		t.Fatal("accepted code with cancelled context")
	}
	if got := s.RecoveryCodesLeft(ctx); got != 2 {
		t.Fatalf("failed attempts consumed codes: %d left", got)
	}
	if !s.useRecoveryCode(ctx, " ABCDE-23456 ") {
		t.Fatal("normalized valid code rejected")
	}
	if s.useRecoveryCode(ctx, "abcde23456") {
		t.Fatal("normalized code replay accepted")
	}
	if !s.useRecoveryCode(ctx, "fghjk23456") {
		t.Fatal("other valid code lost")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if s.useRecoveryCode(ctx, "abcde23456") {
		t.Fatal("accepted code with closed database")
	}
}

func TestRecoveryCodeConsumesDuplicateHashes(t *testing.T) {
	const code = "abcde23456"
	database := recoveryTestDB(t, []string{code, code, "fghjk23456", code})
	s := New(database)
	ctx := context.Background()
	if !s.useRecoveryCode(ctx, code) {
		t.Fatal("valid code rejected")
	}
	if s.useRecoveryCode(ctx, code) {
		t.Fatal("duplicate hash allowed replay")
	}
	if got := s.RecoveryCodesLeft(ctx); got != 1 {
		t.Fatalf("codes remaining = %d; want 1", got)
	}
	if !s.useRecoveryCode(ctx, "fghjk23456") {
		t.Fatal("unrelated code lost")
	}
}

func TestRecoveryCodeRequiresWholeHash(t *testing.T) {
	const code = "abcde23456"
	database := recoveryTestDB(t, nil)
	s := New(database)
	for _, hash := range []string{"prefix" + hashRecovery(code), hashRecovery(code) + "suffix"} {
		if _, err := database.Exec(`UPDATE users SET recovery_codes_hash=? WHERE id='owner'`, hash); err != nil {
			t.Fatal(err)
		}
		if s.useRecoveryCode(context.Background(), code) {
			t.Fatal("accepted partial hash match")
		}
	}
}

func TestRecoveryCodeAcrossDatabaseHandles(t *testing.T) {
	const code = "abcde23456"
	database := recoveryTestDB(t, []string{code})
	var seq int
	var name, path string
	if err := database.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	handles := []*sql.DB{database, other}
	var successes atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if New(handles[i%len(handles)]).useRecoveryCode(context.Background(), code) {
				successes.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if got := successes.Load(); got != 1 {
		t.Fatalf("one code accepted %d times across database handles; want 1", got)
	}
}

func TestRecoveryCodeUpdateFailureDoesNotConsume(t *testing.T) {
	const code = "abcde23456"
	database := recoveryTestDB(t, []string{code})
	if _, err := database.Exec(`CREATE TRIGGER reject_recovery_update BEFORE UPDATE OF recovery_codes_hash ON users BEGIN SELECT RAISE(ABORT, 'test update failure'); END`); err != nil {
		t.Fatal(err)
	}
	s := New(database)
	ctx := context.Background()
	if s.useRecoveryCode(ctx, code) {
		t.Fatal("accepted code after failed update")
	}
	if got := s.RecoveryCodesLeft(ctx); got != 1 {
		t.Fatalf("failed update consumed code: %d left", got)
	}
	if _, err := database.Exec(`DROP TRIGGER reject_recovery_update`); err != nil {
		t.Fatal(err)
	}
	if !s.useRecoveryCode(ctx, code) {
		t.Fatal("code unavailable after failed update")
	}
}

func TestRecoveryCodeRetryHonorsCancellation(t *testing.T) {
	const code = "abcde23456"
	database := recoveryTestDB(t, []string{code})
	// Simulate repeated zero-row updates without changing the original snapshot.
	if _, err := database.Exec(`CREATE TRIGGER ignore_recovery_update BEFORE UPDATE OF recovery_codes_hash ON users BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if New(database).useRecoveryCode(ctx, code) {
		t.Fatal("accepted code without a successful update")
	}
	if ctx.Err() == nil {
		t.Fatal("retry returned before cancellation")
	}
	if got := New(database).RecoveryCodesLeft(context.Background()); got != 1 {
		t.Fatalf("cancelled retries consumed code: %d left", got)
	}
}
