package main

import (
	"sort"
	"strings"

	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
	"github.com/Snipa22/go-tari-lib/v3/address"
)

// 4-tier payout routing policy thresholds, in microMinotari (1 XTM = 1,000,000 µT).
//
//   - Tier1 (individual): balance > tier1IndividualThreshold. Sent one-at-a-time, never batched,
//     highest balance first.
//   - Tier2 (batched):    balance <= tier2SmallThreshold. Batched, lowest balance first.
//   - Tier3 (batched):    tier2SmallThreshold < balance <= tier3MediumThreshold. Batched, highest
//     balance first.
//   - Tier4 (batched):    tier3MediumThreshold < balance <= tier1IndividualThreshold. Batched,
//     highest balance first.
//
// batchAmountCap is the shared cumulative-amount cap applied to every batch built for Tiers 2/3/4.
const (
	tier1IndividualThreshold uint64 = 1_000_000_000
	tier2SmallThreshold      uint64 = 50_000_000
	tier3MediumThreshold     uint64 = 100_000_000
	batchAmountCap           uint64 = 1_000_000_000
)

// PayoutGroup is one unit of work to submit to the wallet: either a single individually-sent
// payment (Tier1) or a batch of payments meant to be sent as a single on-chain transaction
// (Tiers 2/3/4, singleTx=true).
type PayoutGroup struct {
	Tier         int // 1..4
	IsIndividual bool
	Payments     []*tari_generated.PaymentRecipient
	BatchIndex   int // index of this batch within its tier, for dry-run/reporting; 0 for individual
}

// InvalidAddressEntry records a payment excluded from every tier/group because its address
// failed format/cryptographic validation.
type InvalidAddressEntry struct {
	Address string
	Err     error
	Amount  uint64
}

// SplitResult is the full output of splitPayments: the ordered groups ready to submit to the
// wallet, and the payments excluded due to a malformed address (never silently dropped).
type SplitResult struct {
	Groups           []PayoutGroup         // ordered: all Tier1 individuals, then Tier2 batches, then Tier3, then Tier4
	InvalidAddresses []InvalidAddressEntry // excluded entries, for reporting -- never silently dropped
}

// validatePaymentAddress trims raw and runs it through go-tari-lib's address parser, mirroring
// the pattern established in go-tari-faucet's internal/faucet/address.go: trim whitespace,
// reject empty string with address.ErrInvalidAddressString, otherwise defer to address.Parse and
// propagate its error unchanged.
func validatePaymentAddress(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return address.ErrInvalidAddressString
	}
	_, err := address.Parse(trimmed)
	return err
}

// filterValidAddresses splits payments into those with a well-formed address and those without.
// This mirrors the real Tari wallet's gRPC handler, which validates/parses every recipient
// address in a TransferRequest before building any transaction -- under single_tx=true, one bad
// address would otherwise block an entire batch of good payouts, so bad addresses are excluded
// here, before tier assignment, rather than left to fail at the wallet.
//
// Pure: no wallet/SQL/redis side effects, safe to unit test directly.
func filterValidAddresses(payments []*tari_generated.PaymentRecipient) (valid []*tari_generated.PaymentRecipient, invalid []InvalidAddressEntry) {
	valid = make([]*tari_generated.PaymentRecipient, 0, len(payments))
	invalid = make([]InvalidAddressEntry, 0)
	for _, payment := range payments {
		if err := validatePaymentAddress(payment.Address); err != nil {
			invalid = append(invalid, InvalidAddressEntry{
				Address: payment.Address,
				Err:     err,
				Amount:  payment.Amount,
			})
			continue
		}
		valid = append(valid, payment)
	}
	return valid, invalid
}

// buildTierBatches greedily accumulates payments (already in the tier's required sort order)
// into batches, flushing the current batch and starting a new one with the current payment
// whenever adding it would push the running amount sum above batchAmountCap, or the batch's
// payment count to txnsPerBatch. Payments from different tiers are never mixed into the same
// batch -- callers pass one tier's payments at a time.
//
// Pure: no wallet/SQL/redis side effects, safe to unit test directly.
func buildTierBatches(tier int, payments []*tari_generated.PaymentRecipient, txnsPerBatch int) []PayoutGroup {
	groups := make([]PayoutGroup, 0)
	if len(payments) == 0 {
		return groups
	}

	batchIndex := 0
	current := make([]*tari_generated.PaymentRecipient, 0, txnsPerBatch)
	var currentSum uint64

	for _, payment := range payments {
		if len(current) > 0 && (currentSum+payment.Amount > batchAmountCap || len(current) >= txnsPerBatch) {
			groups = append(groups, PayoutGroup{
				Tier:         tier,
				IsIndividual: false,
				Payments:     current,
				BatchIndex:   batchIndex,
			})
			batchIndex++
			current = make([]*tari_generated.PaymentRecipient, 0, txnsPerBatch)
			currentSum = 0
		}
		current = append(current, payment)
		currentSum += payment.Amount
	}
	if len(current) > 0 {
		groups = append(groups, PayoutGroup{
			Tier:         tier,
			IsIndividual: false,
			Payments:     current,
			BatchIndex:   batchIndex,
		})
	}
	return groups
}

// splitPayments implements the full 4-tier payout routing policy: address pre-validation, tier
// classification, per-tier sort order, and greedy batch construction by cumulative amount + count
// cap. It has no wallet/SQL/redis side effects, so it's fully unit-testable without a live wallet
// or database.
//
// Processing order (and the order Groups is returned in):
//  1. Tier1 -- balance > tier1IndividualThreshold: sent individually, one group per payment,
//     highest balance first.
//  2. Tier2 -- balance <= tier2SmallThreshold: batched, lowest balance first.
//  3. Tier3 -- tier2SmallThreshold < balance <= tier3MediumThreshold: batched, highest balance
//     first.
//  4. Tier4 -- tier3MediumThreshold < balance <= tier1IndividualThreshold: batched, highest
//     balance first.
//
// Payments whose address fails validation are excluded entirely (never present in any Group) and
// surfaced via SplitResult.InvalidAddresses instead.
func splitPayments(payments []*tari_generated.PaymentRecipient, txnsPerBatch int) SplitResult {
	validPayments, invalid := filterValidAddresses(payments)

	tier1 := make([]*tari_generated.PaymentRecipient, 0)
	tier2 := make([]*tari_generated.PaymentRecipient, 0)
	tier3 := make([]*tari_generated.PaymentRecipient, 0)
	tier4 := make([]*tari_generated.PaymentRecipient, 0)

	for _, payment := range validPayments {
		switch {
		case payment.Amount > tier1IndividualThreshold:
			tier1 = append(tier1, payment)
		case payment.Amount <= tier2SmallThreshold:
			tier2 = append(tier2, payment)
		case payment.Amount <= tier3MediumThreshold:
			tier3 = append(tier3, payment)
		default:
			tier4 = append(tier4, payment)
		}
	}

	sort.SliceStable(tier1, func(i, j int) bool { return tier1[i].Amount > tier1[j].Amount }) // highest -> lowest
	sort.SliceStable(tier2, func(i, j int) bool { return tier2[i].Amount < tier2[j].Amount }) // lowest -> highest
	sort.SliceStable(tier3, func(i, j int) bool { return tier3[i].Amount > tier3[j].Amount }) // highest -> lowest
	sort.SliceStable(tier4, func(i, j int) bool { return tier4[i].Amount > tier4[j].Amount }) // highest -> lowest

	groups := make([]PayoutGroup, 0, len(tier1)+len(tier2)+len(tier3)+len(tier4))
	for _, payment := range tier1 {
		groups = append(groups, PayoutGroup{
			Tier:         1,
			IsIndividual: true,
			Payments:     []*tari_generated.PaymentRecipient{payment},
			BatchIndex:   0,
		})
	}
	groups = append(groups, buildTierBatches(2, tier2, txnsPerBatch)...)
	groups = append(groups, buildTierBatches(3, tier3, txnsPerBatch)...)
	groups = append(groups, buildTierBatches(4, tier4, txnsPerBatch)...)

	return SplitResult{
		Groups:           groups,
		InvalidAddresses: invalid,
	}
}
