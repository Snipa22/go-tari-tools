package main

import (
	"fmt"

	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
)

// CorrelatedResult pairs one TransferResult with the exact PaymentRecipient (from the same
// SendTransactions call, at the same index) it belongs to.
type CorrelatedResult struct {
	Payment *tari_generated.PaymentRecipient
	Result  *tari_generated.TransferResult
}

// correlateTransferResults pairs each entry in resp.Results with the PaymentRecipient at the
// same index in payments -- the exact slice submitted in that SendTransactions call.
//
// Deliberately does NOT correlate by matching TransferResult.Address against the original
// request: confirmed via live testnet testing (see cmd/payoutDaemon's AGENTS.md/commit history
// for the incident) that the wallet echoes the address back in a DIFFERENT encoding
// (emoji-format) than what was sent whenever singleTx=true, while singleTx=false echoes it back
// verbatim in the original encoding. Index-order between request and response is the only
// correlation the wallet's behavior actually guarantees, confirmed empirically for both
// singleTx values.
//
// Returns an error (touching no data) if len(resp.Results) != len(payments) -- that signals
// something is deeply wrong with the wallet's response, and callers must NOT fall back to
// address-based matching, since that's the exact bug class this function exists to eliminate.
func correlateTransferResults(payments []*tari_generated.PaymentRecipient, resp *tari_generated.TransferResponse) ([]CorrelatedResult, error) {
	results := resp.GetResults()
	if len(results) != len(payments) {
		return nil, fmt.Errorf("correlateTransferResults: length mismatch, %v payments submitted but %v results returned",
			len(payments), len(results))
	}

	correlated := make([]CorrelatedResult, 0, len(payments))
	for i, payment := range payments {
		correlated = append(correlated, CorrelatedResult{
			Payment: payment,
			Result:  results[i],
		})
	}
	return correlated, nil
}
