package sql

import (
	"context"
	"fmt"
	"os"
	"testing"

	core "github.com/Snipa22/core-go-lib/milieu"
)

// defaultTestDSN is a generic local-Postgres guess used only when TEST_DATABASE_URL isn't set.
// It is expected to fail to connect (and the test to skip) in most environments, including CI,
// which has no Postgres service configured -- mirroring the TEST_DATABASE_URL convention already
// established in this org's go-tari-netmap repo.
const defaultTestDSN = "postgres://postgres@localhost:5432/postgres?sslmode=disable"

func testDSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultTestDSN
}

// newTestMilieu returns a Milieu backed by a real Postgres connection for integration testing
// GetAllBalances. If no reachable test database is configured (via TEST_DATABASE_URL or the
// generic local default), the test is skipped rather than failed -- full DB-backed testing isn't
// guaranteed to be possible in every environment this suite runs in.
func newTestMilieu(t *testing.T) *core.Milieu {
	t.Helper()
	dsn := testDSN()
	milieu, err := core.NewMilieu(&dsn, nil, nil)
	if err != nil || milieu.GetRawPGXPool() == nil {
		t.Skipf("skipping: cannot reach test database at %q: %v", dsn, err)
	}
	// Confirm the balances table actually exists and is reachable; if not, skip rather than fail,
	// since provisioning the schema is outside this test's responsibility.
	if _, err := milieu.GetRawPGXPool().Exec(context.Background(), "select 1 from balances limit 1"); err != nil {
		t.Skipf("skipping: balances table not reachable/usable at %q: %v", dsn, err)
	}
	return milieu
}

// insertTestBalance inserts a row into balances with a unique address and registers cleanup to
// remove it after the test completes, regardless of outcome.
func insertTestBalance(t *testing.T, milieu *core.Milieu, address string, balance int64) {
	t.Helper()
	_, err := milieu.GetRawPGXPool().Exec(context.Background(),
		"insert into balances (balance, address) values ($1, $2)", balance, address)
	if err != nil {
		t.Fatalf("failed to insert test balance row (address=%v balance=%v): %v", address, balance, err)
	}
	t.Cleanup(func() {
		_, _ = milieu.GetRawPGXPool().Exec(context.Background(), "delete from balances where address = $1", address)
	})
}

// TestGetAllBalances_ExcludesNegativeBalanceRows is an integration test against a real Postgres
// instance. It requires TEST_DATABASE_URL (or a reachable Postgres at the generic local default)
// pointing at a database with the `balances` table already created (see
// cmd/payoutDaemon/tables.sql). If no such database is reachable, the test skips itself.
func TestGetAllBalances_ExcludesNegativeBalanceRows(t *testing.T) {
	milieu := newTestMilieu(t)

	negativeAddr := fmt.Sprintf("test-negative-%s", uniqueSuffix())
	validAddrA := fmt.Sprintf("test-valid-a-%s", uniqueSuffix())
	validAddrB := fmt.Sprintf("test-valid-b-%s", uniqueSuffix())

	insertTestBalance(t, milieu, negativeAddr, -50)
	insertTestBalance(t, milieu, validAddrA, 100)
	insertTestBalance(t, milieu, validAddrB, 0)

	results, err := GetAllBalances(milieu, 0)
	if err != nil {
		t.Fatalf("GetAllBalances returned an error: %v", err)
	}

	seen := make(map[string]BalanceSqlRow)
	for _, r := range results {
		if r.Address == negativeAddr || r.Address == validAddrA || r.Address == validAddrB {
			seen[r.Address] = r
		}
	}

	if _, ok := seen[negativeAddr]; ok {
		t.Errorf("expected negative-balance row (address=%v) to be excluded, but it was returned", negativeAddr)
	}

	if row, ok := seen[validAddrA]; !ok {
		t.Errorf("expected valid row (address=%v) to be returned, but it was missing", validAddrA)
	} else if row.Balance != 100 {
		t.Errorf("valid row (address=%v) balance = %v, want 100", validAddrA, row.Balance)
	}

	if row, ok := seen[validAddrB]; !ok {
		t.Errorf("expected valid zero-balance row (address=%v) to be returned, but it was missing", validAddrB)
	} else if row.Balance != 0 {
		t.Errorf("valid row (address=%v) balance = %v, want 0", validAddrB, row.Balance)
	}
}

var uniqueCounter int

// uniqueSuffix produces a per-process-unique-enough string for test addresses, avoiding the
// unique index on balances.address colliding across test runs/rows within a single test.
func uniqueSuffix() string {
	uniqueCounter++
	return fmt.Sprintf("%d-%d", os.Getpid(), uniqueCounter)
}
