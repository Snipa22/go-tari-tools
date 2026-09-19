package main

import (
	"errors"
	"testing"

	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
	"github.com/Snipa22/go-tari-lib/address"
)

// Two well-formed base58 Tari addresses (lifted from go-tari-lib's own address_test.go fixtures)
// used wherever a test needs a valid recipient address but doesn't care which one.
const (
	validAddrA  = "12Ncgdgjqo392bLF1YkKNqb9jayc2RyG6wWJupirFG7taXwJguUgrUpUEPZPpK6n66Ytob4asAUc8EnVpS8ckWNMHef"
	validAddrB  = "f2GYDtVpj6yx8ZRPez2fsaU3VBAfVzcYycb3boUqMz1C9cZdJ7CrAkhhYoqRRNJPjwRSKqfd2caRe9jv8ZKwAwDGbvD"
	invalidAddr = "not-a-real-address"
)

func mkPayment(addr string, amount uint64) *tari_generated.PaymentRecipient {
	return &tari_generated.PaymentRecipient{
		Address:    addr,
		Amount:     amount,
		FeePerGram: 5,
	}
}

func amounts(payments []*tari_generated.PaymentRecipient) []uint64 {
	out := make([]uint64, len(payments))
	for i, p := range payments {
		out[i] = p.Amount
	}
	return out
}

func equalUint64Slices(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSplitPayments_TierBoundaries(t *testing.T) {
	tests := []struct {
		name           string
		amount         uint64
		wantTier       int
		wantIndividual bool
	}{
		{"tier2 at threshold", tier2SmallThreshold, 2, false},
		{"tier2 well under threshold", 1_000_000, 2, false},
		{"tier3 just above tier2 threshold", tier2SmallThreshold + 1, 3, false},
		{"tier3 at threshold", tier3MediumThreshold, 3, false},
		{"tier4 just above tier3 threshold", tier3MediumThreshold + 1, 4, false},
		{"tier4 at tier1 threshold", tier1IndividualThreshold, 4, false},
		{"tier1 just above threshold", tier1IndividualThreshold + 1, 1, true},
		{"tier1 well above threshold", 5_000_000_000, 1, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := splitPayments([]*tari_generated.PaymentRecipient{mkPayment(validAddrA, tc.amount)}, 50)
			if len(result.InvalidAddresses) != 0 {
				t.Fatalf("unexpected invalid addresses: %v", result.InvalidAddresses)
			}
			if len(result.Groups) != 1 {
				t.Fatalf("expected exactly 1 group, got %v", len(result.Groups))
			}
			g := result.Groups[0]
			if g.Tier != tc.wantTier {
				t.Errorf("tier = %v, want %v", g.Tier, tc.wantTier)
			}
			if g.IsIndividual != tc.wantIndividual {
				t.Errorf("isIndividual = %v, want %v", g.IsIndividual, tc.wantIndividual)
			}
			if len(g.Payments) != 1 || g.Payments[0].Amount != tc.amount {
				t.Errorf("group payments = %v, want single payment of amount %v", g.Payments, tc.amount)
			}
		})
	}
}

func TestSplitPayments_SortOrderPerTier(t *testing.T) {
	// Tier1: individually sent, highest -> lowest.
	tier1Payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 2_000_000_000),
		mkPayment(validAddrA, 5_000_000_000),
		mkPayment(validAddrA, 3_000_000_000),
	}
	// Tier2: batched, lowest -> highest.
	tier2Payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 30_000_000),
		mkPayment(validAddrA, 10_000_000),
		mkPayment(validAddrA, 20_000_000),
	}
	// Tier3: batched, highest -> lowest.
	tier3Payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 60_000_000),
		mkPayment(validAddrA, 100_000_000),
		mkPayment(validAddrA, 80_000_000),
	}
	// Tier4: batched, highest -> lowest.
	tier4Payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 150_000_000),
		mkPayment(validAddrA, 300_000_000),
		mkPayment(validAddrA, 200_000_000),
	}

	t.Run("tier1 descending, individual", func(t *testing.T) {
		result := splitPayments(tier1Payments, 50)
		if len(result.Groups) != 3 {
			t.Fatalf("expected 3 individual groups, got %v", len(result.Groups))
		}
		want := []uint64{5_000_000_000, 3_000_000_000, 2_000_000_000}
		for i, g := range result.Groups {
			if !g.IsIndividual || g.Tier != 1 {
				t.Errorf("group %v: expected individual tier1 group, got tier=%v individual=%v", i, g.Tier, g.IsIndividual)
			}
			if len(g.Payments) != 1 || g.Payments[0].Amount != want[i] {
				t.Errorf("group %v amount = %v, want %v", i, amounts(g.Payments), want[i])
			}
		}
	})

	t.Run("tier2 ascending, batched", func(t *testing.T) {
		result := splitPayments(tier2Payments, 50)
		if len(result.Groups) != 1 {
			t.Fatalf("expected 1 batch, got %v", len(result.Groups))
		}
		want := []uint64{10_000_000, 20_000_000, 30_000_000}
		if !equalUint64Slices(amounts(result.Groups[0].Payments), want) {
			t.Errorf("batch order = %v, want %v", amounts(result.Groups[0].Payments), want)
		}
	})

	t.Run("tier3 descending, batched", func(t *testing.T) {
		result := splitPayments(tier3Payments, 50)
		if len(result.Groups) != 1 {
			t.Fatalf("expected 1 batch, got %v", len(result.Groups))
		}
		want := []uint64{100_000_000, 80_000_000, 60_000_000}
		if !equalUint64Slices(amounts(result.Groups[0].Payments), want) {
			t.Errorf("batch order = %v, want %v", amounts(result.Groups[0].Payments), want)
		}
	})

	t.Run("tier4 descending, batched", func(t *testing.T) {
		result := splitPayments(tier4Payments, 50)
		if len(result.Groups) != 1 {
			t.Fatalf("expected 1 batch, got %v", len(result.Groups))
		}
		want := []uint64{300_000_000, 200_000_000, 150_000_000}
		if !equalUint64Slices(amounts(result.Groups[0].Payments), want) {
			t.Errorf("batch order = %v, want %v", amounts(result.Groups[0].Payments), want)
		}
	})
}

func TestSplitPayments_MultiBatchByAmountCap(t *testing.T) {
	// Two tier4 payments of 600M each: sum would be 1.2B > batchAmountCap (1B), so they must
	// land in two separate batches even though the count cap (50) is nowhere near being hit.
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 600_000_000),
		mkPayment(validAddrB, 600_000_000),
	}
	result := splitPayments(payments, 50)
	if len(result.InvalidAddresses) != 0 {
		t.Fatalf("unexpected invalid addresses: %v", result.InvalidAddresses)
	}
	if len(result.Groups) != 2 {
		t.Fatalf("expected 2 batches (amount cap forces split), got %v", len(result.Groups))
	}
	for i, g := range result.Groups {
		if g.IsIndividual || g.Tier != 4 {
			t.Errorf("group %v: expected batched tier4 group, got tier=%v individual=%v", i, g.Tier, g.IsIndividual)
		}
		if g.BatchIndex != i {
			t.Errorf("group %v: BatchIndex = %v, want %v", i, g.BatchIndex, i)
		}
		if len(g.Payments) != 1 {
			t.Errorf("group %v: expected exactly 1 payment (cap forces a new batch per payment here), got %v", i, len(g.Payments))
		}
	}
}

func TestSplitPayments_MultiBatchByCountCap(t *testing.T) {
	// 4 small tier2 payments with a txnsPerBatch of 2: the amount cap is nowhere near hit
	// (sum = 10M), but the count cap must still force 2 batches of 2.
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 4_000_000),
		mkPayment(validAddrA, 3_000_000),
		mkPayment(validAddrA, 2_000_000),
		mkPayment(validAddrA, 1_000_000),
	}
	result := splitPayments(payments, 2)
	if len(result.Groups) != 2 {
		t.Fatalf("expected 2 batches (count cap forces split), got %v", len(result.Groups))
	}
	wantBatch0 := []uint64{1_000_000, 2_000_000} // ascending sort, then greedily filled
	wantBatch1 := []uint64{3_000_000, 4_000_000}
	if !equalUint64Slices(amounts(result.Groups[0].Payments), wantBatch0) {
		t.Errorf("batch 0 = %v, want %v", amounts(result.Groups[0].Payments), wantBatch0)
	}
	if !equalUint64Slices(amounts(result.Groups[1].Payments), wantBatch1) {
		t.Errorf("batch 1 = %v, want %v", amounts(result.Groups[1].Payments), wantBatch1)
	}
	if result.Groups[0].BatchIndex != 0 || result.Groups[1].BatchIndex != 1 {
		t.Errorf("batch indexes = %v, %v, want 0, 1", result.Groups[0].BatchIndex, result.Groups[1].BatchIndex)
	}
}

func TestSplitPayments_MixedAllTiers(t *testing.T) {
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 2_000_000_000), // tier1
		mkPayment(validAddrA, 10_000_000),    // tier2
		mkPayment(validAddrA, 80_000_000),    // tier3
		mkPayment(validAddrA, 300_000_000),   // tier4
	}
	result := splitPayments(payments, 50)
	if len(result.InvalidAddresses) != 0 {
		t.Fatalf("unexpected invalid addresses: %v", result.InvalidAddresses)
	}
	if len(result.Groups) != 4 {
		t.Fatalf("expected 4 groups (1 per tier), got %v", len(result.Groups))
	}

	wantTierOrder := []int{1, 2, 3, 4}
	for i, g := range result.Groups {
		if g.Tier != wantTierOrder[i] {
			t.Errorf("group %v: tier = %v, want %v (Tier1 individuals must come before ANY Tier2 batch, etc.)", i, g.Tier, wantTierOrder[i])
		}
		// No cross-tier mixing within a batch: every payment in this group must be a payment
		// we know belongs to this tier.
		for _, p := range g.Payments {
			gotTier := tierOf(p.Amount)
			if gotTier != g.Tier {
				t.Errorf("group %v (tier %v) contains a payment of amount %v which belongs to tier %v -- cross-tier mixing", i, g.Tier, p.Amount, gotTier)
			}
		}
	}
	if !result.Groups[0].IsIndividual {
		t.Errorf("tier1 group should be individual")
	}
	for _, g := range result.Groups[1:] {
		if g.IsIndividual {
			t.Errorf("tier %v group should be batched, not individual", g.Tier)
		}
	}
}

// tierOf is a small test-only mirror of the tier-classification switch in splitPayments, used
// only to assert no cross-tier mixing happened.
func tierOf(amount uint64) int {
	switch {
	case amount > tier1IndividualThreshold:
		return 1
	case amount <= tier2SmallThreshold:
		return 2
	case amount <= tier3MediumThreshold:
		return 3
	default:
		return 4
	}
}

func TestSplitPayments_InvalidAddressExcluded(t *testing.T) {
	// A huge invalid-address payment sits between two small valid tier2 payments in input
	// order. If its amount ever leaked into the cumulative-amount batch math, it would force
	// an extra batch split (or otherwise perturb the cap check) -- it must not.
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 40_000_000),
		mkPayment(invalidAddr, 999_000_000),
		mkPayment(validAddrB, 45_000_000),
	}
	result := splitPayments(payments, 50)

	if len(result.InvalidAddresses) != 1 {
		t.Fatalf("expected exactly 1 invalid address entry, got %v", len(result.InvalidAddresses))
	}
	invalidEntry := result.InvalidAddresses[0]
	if invalidEntry.Address != invalidAddr {
		t.Errorf("invalid entry address = %q, want %q", invalidEntry.Address, invalidAddr)
	}
	if invalidEntry.Amount != 999_000_000 {
		t.Errorf("invalid entry amount = %v, want %v", invalidEntry.Amount, 999_000_000)
	}
	if !errors.Is(invalidEntry.Err, address.ErrInvalidAddressString) {
		t.Errorf("invalid entry err = %v, want wrapping address.ErrInvalidAddressString", invalidEntry.Err)
	}

	// The invalid entry must never show up in any group.
	for _, g := range result.Groups {
		for _, p := range g.Payments {
			if p.Address == invalidAddr {
				t.Errorf("invalid address payment leaked into group (tier %v, batch %v)", g.Tier, g.BatchIndex)
			}
		}
	}

	// The two valid payments (sum 85M, well under batchAmountCap) must land together in a
	// single tier2 batch -- proving the excluded 999M amount did not contribute to the
	// cumulative-amount cap check.
	if len(result.Groups) != 1 {
		t.Fatalf("expected exactly 1 group for the 2 remaining valid payments, got %v", len(result.Groups))
	}
	g := result.Groups[0]
	if g.Tier != 2 || g.IsIndividual {
		t.Errorf("expected a batched tier2 group, got tier=%v individual=%v", g.Tier, g.IsIndividual)
	}
	want := []uint64{40_000_000, 45_000_000}
	if !equalUint64Slices(amounts(g.Payments), want) {
		t.Errorf("group payments = %v, want %v", amounts(g.Payments), want)
	}
}

func TestSplitPayments_AllAddressesInvalidInTier(t *testing.T) {
	// Every payment here is tier3-ranged but has a malformed address -- the tier must produce
	// zero groups, not error out or panic.
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(invalidAddr, 60_000_000),
		mkPayment("also not real", 70_000_000),
		mkPayment("", 80_000_000),
	}

	result := splitPayments(payments, 50)

	if len(result.Groups) != 0 {
		t.Fatalf("expected zero groups when every address in the tier is malformed, got %v", len(result.Groups))
	}
	if len(result.InvalidAddresses) != 3 {
		t.Fatalf("expected all 3 payments to be recorded as invalid, got %v", len(result.InvalidAddresses))
	}
}

func TestFilterValidAddresses(t *testing.T) {
	payments := []*tari_generated.PaymentRecipient{
		mkPayment(validAddrA, 1_000_000),
		mkPayment(invalidAddr, 2_000_000),
		mkPayment("  ", 3_000_000),
		mkPayment(validAddrB, 4_000_000),
	}

	valid, invalid := filterValidAddresses(payments)

	if len(valid) != 2 {
		t.Fatalf("expected 2 valid payments, got %v", len(valid))
	}
	if valid[0].Address != validAddrA || valid[1].Address != validAddrB {
		t.Errorf("valid payments = %v, want [%v, %v]", amounts(valid), validAddrA, validAddrB)
	}
	if len(invalid) != 2 {
		t.Fatalf("expected 2 invalid entries, got %v", len(invalid))
	}
	for _, entry := range invalid {
		if !errors.Is(entry.Err, address.ErrInvalidAddressString) {
			t.Errorf("invalid entry err = %v, want wrapping address.ErrInvalidAddressString", entry.Err)
		}
	}
}
