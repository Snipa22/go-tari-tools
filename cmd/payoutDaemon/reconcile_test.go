package main

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

// fakeScanDeleter is an in-memory redisScanDeleter test double, mirroring the
// fakeRedisFlagSetter pattern in wallet_test.go: it holds a fixed key space and supports
// cursor-based Scan pagination plus Del, without needing a real Redis server or a
// miniredis-style dependency.
type fakeScanDeleter struct {
	mu sync.Mutex
	// keys is the full set of keys currently "in Redis", used both as the source Scan pages
	// through and as the record Del mutates.
	keys map[string]struct{}
	// pageSize controls how many keys Scan returns per call, to exercise cursor pagination
	// across multiple Scan calls the same way a real Redis SCAN would for a large keyspace.
	pageSize int
	// snapshot is the stable, sorted key list a scan pass pages through, captured once at
	// cursor 0 and reused until the scan completes -- mirrors real Redis SCAN semantics where
	// the cursor stays valid and doesn't skip/duplicate entries even as keys are deleted
	// mid-scan, unlike re-deriving the list fresh from f.keys on every call (which would shift
	// indexes out from under an in-flight cursor as Del removes entries).
	snapshot []string
	// scanErr, if non-nil, is returned by the Scan call at scanErrOnCall (1-indexed).
	scanErr       error
	scanErrOnCall int
	scanCalls     int
	// delErrFor, if non-nil, is returned as the error for a Del call on the given key,
	// leaving the key in place (as it would if the delete had genuinely failed).
	delErrFor map[string]error
	delCalls  []string
}

func newFakeScanDeleter(keys ...string) *fakeScanDeleter {
	f := &fakeScanDeleter{keys: make(map[string]struct{}), pageSize: 100}
	for _, k := range keys {
		f.keys[k] = struct{}{}
	}
	return f
}

// Scan implements a minimal, deterministic cursor protocol over f.keys: cursor is simply the
// index into a stable, sorted snapshot of the current key set at the time of the first call.
// This is sufficient to exercise multi-page pagination without depending on Go map iteration
// order across calls.
func (f *fakeScanDeleter) Scan(_ context.Context, cursor uint64, match string, _ int64) *redis.ScanCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scanCalls++
	cmd := redis.NewScanCmd(context.Background(), nil)
	if f.scanErr != nil && f.scanCalls == f.scanErrOnCall {
		cmd.SetErr(f.scanErr)
		return cmd
	}

	// Snapshot + sort every key at the start of a scan pass (cursor 0) so pagination is
	// deterministic and stable even as Del removes entries mid-scan; reuse that snapshot for
	// every subsequent page of the same pass.
	if cursor == 0 {
		remaining := make([]string, 0, len(f.keys))
		for k := range f.keys {
			remaining = append(remaining, k)
		}
		sortStrings(remaining)
		f.snapshot = remaining
	}
	snapshot := f.snapshot

	matched := make([]string, 0, len(snapshot))
	for _, k := range snapshot {
		if matchGlob(match, k) {
			matched = append(matched, k)
		}
	}

	start := int(cursor)
	if start > len(matched) {
		start = len(matched)
	}
	end := start + f.pageSize
	if end > len(matched) {
		end = len(matched)
	}
	page := matched[start:end]
	nextCursor := uint64(end)
	if end >= len(matched) {
		nextCursor = 0
	}
	cmd.SetVal(page, nextCursor)
	return cmd
}

func (f *fakeScanDeleter) Del(_ context.Context, keysToDelete ...string) *redis.IntCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := redis.NewIntCmd(context.Background())
	var removed int64
	for _, k := range keysToDelete {
		f.delCalls = append(f.delCalls, k)
		if err, ok := f.delErrFor[k]; ok && err != nil {
			cmd.SetErr(err)
			return cmd
		}
		if _, ok := f.keys[k]; ok {
			delete(f.keys, k)
			removed++
		}
	}
	cmd.SetVal(removed)
	return cmd
}

var _ redisScanDeleter = (*fakeScanDeleter)(nil)

// sortStrings/matchGlob are tiny local helpers so this test file doesn't need to pull in extra
// dependencies just for a `prefix*` glob match and a sort.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func matchGlob(pattern, s string) bool {
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(s) >= len(prefix) && s[:len(prefix)] == prefix
	}
	return pattern == s
}

func TestClearAllReconcilePending_ClearsOnlyMatchingKeys(t *testing.T) {
	milieu := newTestMilieu(t)
	fake := newFakeScanDeleter(
		reconcilePendingKey(validAddrA),
		reconcilePendingKey(validAddrB),
		"bal_bypass_"+validAddrA,
		"payout-daemon-halt-batching",
	)

	clearAllReconcilePendingWithClient(milieu, fake)

	if _, ok := fake.keys[reconcilePendingKey(validAddrA)]; ok {
		t.Errorf("expected %v's reconcile-pending key to be cleared", validAddrA)
	}
	if _, ok := fake.keys[reconcilePendingKey(validAddrB)]; ok {
		t.Errorf("expected %v's reconcile-pending key to be cleared", validAddrB)
	}
	if _, ok := fake.keys["bal_bypass_"+validAddrA]; !ok {
		t.Errorf("bal_bypass key must survive untouched, it did not")
	}
	if _, ok := fake.keys["payout-daemon-halt-batching"]; !ok {
		t.Errorf("halt-txn key must survive untouched, it did not")
	}
	if len(fake.delCalls) != 2 {
		t.Errorf("expected exactly 2 Del calls, got %v: %v", len(fake.delCalls), fake.delCalls)
	}
}

func TestClearAllReconcilePending_NoMatchingKeys_ClearsNothing(t *testing.T) {
	milieu := newTestMilieu(t)
	fake := newFakeScanDeleter("bal_bypass_" + validAddrA)

	clearAllReconcilePendingWithClient(milieu, fake)

	if len(fake.delCalls) != 0 {
		t.Errorf("expected no Del calls when nothing matches the reconcile-pending pattern, got %v", fake.delCalls)
	}
	if _, ok := fake.keys["bal_bypass_"+validAddrA]; !ok {
		t.Errorf("unrelated key must survive untouched, it did not")
	}
}

func TestClearAllReconcilePending_PaginatesAcrossMultiplePages(t *testing.T) {
	milieu := newTestMilieu(t)
	fake := newFakeScanDeleter(
		reconcilePendingKey("addr-1"),
		reconcilePendingKey("addr-2"),
		reconcilePendingKey("addr-3"),
	)
	fake.pageSize = 1 // force multiple Scan calls to page through all 3 keys

	clearAllReconcilePendingWithClient(milieu, fake)

	if len(fake.keys) != 0 {
		t.Errorf("expected every reconcile-pending key to be cleared across pages, %v remain", len(fake.keys))
	}
	if fake.scanCalls < 3 {
		t.Errorf("expected pagination to require multiple Scan calls, got %v", fake.scanCalls)
	}
	if len(fake.delCalls) != 3 {
		t.Errorf("expected exactly 3 Del calls, got %v", len(fake.delCalls))
	}
}

func TestClearAllReconcilePending_DelErrorOnOneKeyDoesNotStopTheRest(t *testing.T) {
	milieu := newTestMilieu(t)
	keyA := reconcilePendingKey(validAddrA)
	keyB := reconcilePendingKey(validAddrB)
	fake := newFakeScanDeleter(keyA, keyB)
	fake.delErrFor = map[string]error{keyA: errors.New("redis is on fire")}

	clearAllReconcilePendingWithClient(milieu, fake)

	if _, ok := fake.keys[keyA]; !ok {
		t.Errorf("expected %v to remain since its Del failed", validAddrA)
	}
	if _, ok := fake.keys[keyB]; ok {
		t.Errorf("expected %v to be cleared despite %v's Del failing", validAddrB, validAddrA)
	}
}

func TestClearAllReconcilePending_ScanErrorAborts(t *testing.T) {
	milieu := newTestMilieu(t)
	fake := newFakeScanDeleter(reconcilePendingKey(validAddrA), reconcilePendingKey(validAddrB))
	fake.pageSize = 1
	fake.scanErr = errors.New("redis connection reset")
	fake.scanErrOnCall = 2 // succeed on the first page, fail on the second

	clearAllReconcilePendingWithClient(milieu, fake)

	// The first key should have been cleared before the Scan error aborted the run; the
	// second should still be present since we never got a page containing it.
	if len(fake.keys) != 1 {
		t.Errorf("expected exactly 1 key to remain after the aborted scan, got %v", len(fake.keys))
	}
}
