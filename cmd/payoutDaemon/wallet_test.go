package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	core "github.com/Snipa22/core-go-lib/milieu"
	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
	"github.com/Snipa22/go-tari-lib/v3/walletGRPC"
	"github.com/redis/go-redis/v9"
)

// fakeWalletSender is an in-memory WalletSender test double, mirroring the fakeWallet pattern
// already established in go-tari-faucet/internal/faucet/service_test.go.
type fakeWalletSender struct {
	resp *tari_generated.TransferResponse
	err  error

	mu             sync.Mutex
	sentRecipients [][]*tari_generated.PaymentRecipient
	sentSingleTx   []bool
	sentCtx        []context.Context
}

func (f *fakeWalletSender) SendTransactions(ctx context.Context, transactions []*tari_generated.PaymentRecipient, singleTx bool) (*tari_generated.TransferResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentRecipients = append(f.sentRecipients, transactions)
	f.sentSingleTx = append(f.sentSingleTx, singleTx)
	f.sentCtx = append(f.sentCtx, ctx)
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

var _ WalletSender = (*fakeWalletSender)(nil)

// fakeRedisFlagSetter is an in-memory redisFlagSetter test double: it records every key/value
// pair that flagForReconciliation (or code exercising it) tries to Set, without needing a real
// Redis server or a miniredis-style dependency (neither of which is present anywhere in the
// Snipa22/* ecosystem's go.sum, checked before writing this).
type fakeRedisFlagSetter struct {
	// errFor, if non-nil, is returned as the error for any Set call whose key matches one of
	// its entries -- lets a test inject a failure for one specific address without failing
	// every Set call in the group.
	errFor map[string]error

	mu   sync.Mutex
	sets map[string]interface{}
}

func newFakeRedisFlagSetter() *fakeRedisFlagSetter {
	return &fakeRedisFlagSetter{sets: make(map[string]interface{})}
}

func (f *fakeRedisFlagSetter) Set(_ context.Context, key string, value interface{}, _ time.Duration) *redis.StatusCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := redis.NewStatusCmd(context.Background())
	if err, ok := f.errFor[key]; ok && err != nil {
		cmd.SetErr(err)
		return cmd
	}
	f.sets[key] = value
	cmd.SetVal("OK")
	return cmd
}

var _ redisFlagSetter = (*fakeRedisFlagSetter)(nil)

func newTestMilieu(t *testing.T) *core.Milieu {
	t.Helper()
	// All three connection strings are nil, so NewMilieu skips dialing Postgres/Redis/Sentry
	// entirely -- Info/Debug/Error/CaptureException still work (they just log via logrus, with
	// CaptureException falling back to Error() since sentry is unconfigured), which is all
	// sendGroupPayments/flagForReconciliation need from it in these tests.
	milieu, err := core.NewMilieu(nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error constructing test Milieu: %v", err)
	}
	return milieu
}

func successResponse(txIDs ...uint64) *tari_generated.TransferResponse {
	results := make([]*tari_generated.TransferResult, len(txIDs))
	for i, id := range txIDs {
		results[i] = &tari_generated.TransferResult{IsSuccess: true, TransactionId: id}
	}
	return &tari_generated.TransferResponse{Results: results}
}

func TestReconcilePendingKey(t *testing.T) {
	got := reconcilePendingKey("some-address")
	want := "payout_reconcile_pending_some-address"
	if got != want {
		t.Fatalf("reconcilePendingKey() = %v, want %v", got, want)
	}
}

func TestFlagForReconciliation_SetsKeyForEveryPayment(t *testing.T) {
	setter := newFakeRedisFlagSetter()
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 1_000_000),
		mkPayment(validAddrB, 2_000_000),
	}

	if err := flagForReconciliation(setter, payments); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, p := range payments {
		if _, ok := setter.sets[reconcilePendingKey(p.Address)]; !ok {
			t.Errorf("expected reconcile-pending key to be set for %v, it wasn't", p.Address)
		}
	}
	if len(setter.sets) != len(payments) {
		t.Errorf("expected exactly %v keys set, got %v", len(payments), len(setter.sets))
	}
}

func TestFlagForReconciliation_PropagatesFirstError(t *testing.T) {
	setter := newFakeRedisFlagSetter()
	boom := errors.New("redis is on fire")
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 1_000_000),
		mkPayment(validAddrB, 2_000_000),
	}
	setter.errFor = map[string]error{reconcilePendingKey(validAddrA): boom}

	err := flagForReconciliation(setter, payments)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("expected returned error to wrap %v, got %v", boom, err)
	}
	// The failure on the first address must not stop the second from being flagged.
	if _, ok := setter.sets[reconcilePendingKey(validAddrB)]; !ok {
		t.Errorf("expected %v to still be flagged despite %v failing", validAddrB, validAddrA)
	}
}

func TestSendGroupPayments_Success_DoesNotFlagAnything(t *testing.T) {
	milieu := newTestMilieu(t)
	sender := &fakeWalletSender{resp: successResponse(1, 2)}
	setter := newFakeRedisFlagSetter()
	group := PayoutGroup{
		Tier:     2,
		Payments: []*tari_generated.PaymentRecipient{mkPayment(validAddrA, 1_000_000), mkPayment(validAddrB, 2_000_000)},
	}

	resp, err := sendGroupPayments(milieu, sender, setter, time.Second, group, 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != sender.resp {
		t.Fatalf("expected the sender's response to be returned unchanged")
	}
	if len(setter.sets) != 0 {
		t.Fatalf("a successful send must not flag anything for reconciliation, got %v flagged", len(setter.sets))
	}
}

func TestSendGroupPayments_NonAmbiguousError_DoesNotFlagAnything(t *testing.T) {
	milieu := newTestMilieu(t)
	sender := &fakeWalletSender{err: errors.New("recipient address rejected by wallet")}
	setter := newFakeRedisFlagSetter()
	group := PayoutGroup{
		Tier:     3,
		Payments: []*tari_generated.PaymentRecipient{mkPayment(validAddrA, 1_000_000)},
	}

	_, err := sendGroupPayments(milieu, sender, setter, time.Second, group, 7)
	if err == nil {
		t.Fatal("expected the underlying error to be returned")
	}
	if errors.Is(err, walletGRPC.ErrAmbiguousBroadcast) {
		t.Fatal("a genuinely non-ambiguous error must not be classified as ambiguous")
	}
	if len(setter.sets) != 0 {
		t.Fatalf("a non-ambiguous error must not flag anything for reconciliation, got %v flagged", len(setter.sets))
	}
}

func TestSendGroupPayments_AmbiguousBroadcast_FlagsEveryRecipient(t *testing.T) {
	milieu := newTestMilieu(t)
	underlying := errors.New("context deadline exceeded")
	ambiguousErr := fmt.Errorf("%w: %v", walletGRPC.ErrAmbiguousBroadcast, underlying)
	sender := &fakeWalletSender{err: ambiguousErr}
	setter := newFakeRedisFlagSetter()
	group := PayoutGroup{
		Tier:       4,
		BatchIndex: 3,
		Payments: []*tari_generated.PaymentRecipient{
			mkPayment(validAddrA, 1_000_000),
			mkPayment(validAddrB, 2_000_000),
		},
	}

	_, err := sendGroupPayments(milieu, sender, setter, time.Second, group, 99)
	if err == nil {
		t.Fatal("expected the ambiguous-broadcast error to be returned")
	}
	if !errors.Is(err, walletGRPC.ErrAmbiguousBroadcast) {
		t.Fatalf("expected errors.Is(err, ErrAmbiguousBroadcast) to be true, got %v", err)
	}

	for _, p := range group.Payments {
		if _, ok := setter.sets[reconcilePendingKey(p.Address)]; !ok {
			t.Errorf("expected %v to be flagged for reconciliation after an ambiguous broadcast, it wasn't", p.Address)
		}
	}
	if len(setter.sets) != len(group.Payments) {
		t.Errorf("expected exactly %v keys set, got %v", len(group.Payments), len(setter.sets))
	}
}

func TestSendGroupPayments_UsesTimeoutBoundContext(t *testing.T) {
	milieu := newTestMilieu(t)
	sender := &fakeWalletSender{resp: successResponse(1)}
	setter := newFakeRedisFlagSetter()
	group := PayoutGroup{Payments: []*tari_generated.PaymentRecipient{mkPayment(validAddrA, 1_000_000)}}

	if _, err := sendGroupPayments(milieu, sender, setter, 50*time.Millisecond, group, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sender.sentCtx) != 1 {
		t.Fatalf("expected exactly one SendTransactions call, got %v", len(sender.sentCtx))
	}
	if _, ok := sender.sentCtx[0].Deadline(); !ok {
		t.Fatal("expected the context passed to SendTransactions to carry a deadline")
	}
}
