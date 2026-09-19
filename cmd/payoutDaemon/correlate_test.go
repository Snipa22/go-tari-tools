package main

import (
	"testing"

	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
)

func mkTransferResult(addr string, txID uint64, success bool) *tari_generated.TransferResult {
	return &tari_generated.TransferResult{
		Address:       addr,
		TransactionId: txID,
		IsSuccess:     success,
	}
}

func TestCorrelateTransferResults_EmojiAddressMismatch(t *testing.T) {
	// The exact bug scenario: Results[i].Address is a completely different string than
	// payments[i].Address (simulating the emoji-format re-encoding under singleTx=true).
	// correlateTransferResults must still pair each result with the correct payment by
	// index, regardless of the address mismatch.
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 1_000_000),
		mkPayment(validAddrB, 2_000_000),
	}
	resp := &tari_generated.TransferResponse{
		Results: []*tari_generated.TransferResult{
			mkTransferResult("🍗📟💋🎯🧩🍬🍟🎲", 111, true),
			mkTransferResult("🐙🦄🍩🍕🍦🍇🍒🥑", 222, true),
		},
	}

	got, err := correlateTransferResults(payments, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 correlated results, got %v", len(got))
	}
	if got[0].Payment != payments[0] || got[0].Result != resp.Results[0] {
		t.Errorf("index 0: expected payment=%v result=%v, got payment=%v result=%v",
			payments[0], resp.Results[0], got[0].Payment, got[0].Result)
	}
	if got[1].Payment != payments[1] || got[1].Result != resp.Results[1] {
		t.Errorf("index 1: expected payment=%v result=%v, got payment=%v result=%v",
			payments[1], resp.Results[1], got[1].Payment, got[1].Result)
	}
	// Explicitly confirm the address mismatch is real -- this is the whole point of the test.
	if got[0].Payment.Address == got[0].Result.Address {
		t.Errorf("expected address mismatch at index 0, but they matched")
	}
	if got[1].Payment.Address == got[1].Result.Address {
		t.Errorf("expected address mismatch at index 1, but they matched")
	}
}

func TestCorrelateTransferResults_MatchingAddresses(t *testing.T) {
	// Regression guard for the singleTx=false case: addresses DO happen to match. The fix
	// must not break this previously-working path.
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 1_000_000),
		mkPayment(validAddrB, 2_000_000),
	}
	resp := &tari_generated.TransferResponse{
		Results: []*tari_generated.TransferResult{
			mkTransferResult(validAddrA, 111, true),
			mkTransferResult(validAddrB, 222, false),
		},
	}

	got, err := correlateTransferResults(payments, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 correlated results, got %v", len(got))
	}
	for i, cr := range got {
		if cr.Payment.Address != cr.Result.Address {
			t.Errorf("index %v: expected matching addresses, payment=%v result=%v", i, cr.Payment.Address, cr.Result.Address)
		}
	}
	if got[0].Result.TransactionId != 111 || got[1].Result.TransactionId != 222 {
		t.Errorf("unexpected transaction IDs: %v, %v", got[0].Result.TransactionId, got[1].Result.TransactionId)
	}
}

func TestCorrelateTransferResults_LengthMismatch(t *testing.T) {
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 1_000_000),
		mkPayment(validAddrB, 2_000_000),
	}
	resp := &tari_generated.TransferResponse{
		Results: []*tari_generated.TransferResult{
			mkTransferResult(validAddrA, 111, true),
		},
	}

	got, err := correlateTransferResults(payments, resp)
	if err == nil {
		t.Fatalf("expected error for length mismatch, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty/nil result slice on error, got %v entries", len(got))
	}
}

func TestCorrelateTransferResults_EmptyInputs(t *testing.T) {
	// Zero payments + zero results is a valid degenerate case (lengths match), not an error.
	payments := []*tari_generated.PaymentRecipient{}
	resp := &tari_generated.TransferResponse{
		Results: []*tari_generated.TransferResult{},
	}

	got, err := correlateTransferResults(payments, resp)
	if err != nil {
		t.Fatalf("unexpected error for empty inputs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result slice, got %v entries", len(got))
	}
}

func TestCorrelateTransferResults_OrderPreservation(t *testing.T) {
	// Multiple payments with distinct identifying data (differing Amount) must end up
	// correlated with the result at the SAME index, not sorted/reordered by any other field.
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 500_000_000),
		mkPayment(validAddrB, 1_000_000),
		mkPayment(validAddrA, 999_000_000),
		mkPayment(validAddrB, 42),
	}
	resp := &tari_generated.TransferResponse{
		Results: []*tari_generated.TransferResult{
			mkTransferResult("irrelevant-0", 10, true),
			mkTransferResult("irrelevant-1", 20, false),
			mkTransferResult("irrelevant-2", 30, true),
			mkTransferResult("irrelevant-3", 40, true),
		},
	}

	got, err := correlateTransferResults(payments, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(payments) {
		t.Fatalf("expected %v correlated results, got %v", len(payments), len(got))
	}
	for i := range payments {
		if got[i].Payment != payments[i] {
			t.Errorf("index %v: payment pointer mismatch, expected amount %v, got %v", i, payments[i].Amount, got[i].Payment.Amount)
		}
		if got[i].Result != resp.Results[i] {
			t.Errorf("index %v: result pointer mismatch, expected txid %v, got %v", i, resp.Results[i].TransactionId, got[i].Result.TransactionId)
		}
	}
}
