package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	core "github.com/Snipa22/core-go-lib/milieu"
	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
	"github.com/Snipa22/go-tari-lib/v3/walletGRPC"
	"github.com/redis/go-redis/v9"
)

// reconcilePendingKeyPrefix/reconcilePendingKeyPattern namespace the ambiguous-broadcast
// reconciliation flag, distinct from the existing `bal_bypass_<address>` pattern used for
// operator-approved payout-minimum bypasses. Unlike bal_bypass, these keys are set with NO
// expiry/TTL -- they must persist until a human operator clears them via -clear-reconcile-flag,
// since letting one silently expire and re-enable payout on an address whose broadcast status is
// still unknown would defeat the entire point of this gate.
const reconcilePendingKeyPrefix = "payout_reconcile_pending_"
const reconcilePendingKeyPattern = reconcilePendingKeyPrefix + "*"

// reconcilePendingKey builds the Redis key used to flag address as needing manual reconciliation
// before it's eligible for another payout attempt. Pure function, safe to unit test directly.
func reconcilePendingKey(address string) string {
	return fmt.Sprintf("%v%v", reconcilePendingKeyPrefix, address)
}

// redisFlagSetter is the narrow Redis surface flagForReconciliation depends on -- just the Set
// call -- so tests can inject an in-memory fake instead of requiring a real Redis server or a
// miniredis-style dependency (checked go.sum across the Snipa22 repos ecosystem; no such
// dependency is present anywhere, and adding one was out of scope for this task). Satisfied
// directly by *redis.Client.
type redisFlagSetter interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
}

var _ redisFlagSetter = (*redis.Client)(nil)

// flagForReconciliation sets the reconcile-pending key (no TTL, per reconcilePendingKey's doc
// comment) for every recipient in payments, storing the UTC time the flag was set as the value
// so an operator running -list-reconcile-pending can see when each flag was raised. Continues
// past individual Set failures so one bad key doesn't stop the rest of the group from being
// flagged, returning the first error encountered (if any) once every recipient has been
// attempted.
func flagForReconciliation(redisClient redisFlagSetter, payments []*tari_generated.PaymentRecipient) error {
	ctx := context.Background()
	setAt := time.Now().UTC().Format(time.RFC3339)
	var firstErr error
	for _, payment := range payments {
		if err := redisClient.Set(ctx, reconcilePendingKey(payment.Address), setAt, 0).Err(); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("flagForReconciliation: failed to set reconcile-pending flag for %v: %w", payment.Address, err)
			}
		}
	}
	return firstErr
}

// sendGroupPayments submits one PayoutGroup's payments to the wallet via sender, using a fresh
// timeout-bound context scoped to just this call -- one context per group send, not one shared
// across the whole run, so each group's send gets its own timeout window.
//
// If the send fails with an ambiguous-broadcast error (errors.Is(err,
// walletGRPC.ErrAmbiguousBroadcast) -- a transport failure that gives no signal on whether the
// wallet actually broadcast the transaction before failing), every recipient in the group is
// flagged for manual reconciliation via flagForReconciliation, and the failure is logged loudly
// (milieu.Error, not just Info, plus milieu.CaptureException) with the full tier/batch/recipient
// context an operator needs to go check the wallet's actual transaction history. The group's
// balances are NOT decremented in this path -- same as every other error path today, this
// function never touches balances itself; that only happens in atomicBalanceUpdates, which the
// caller does not invoke when this function returns a non-nil error.
//
// Depends on WalletSender, not the concrete *walletGRPC.Client, so this is unit-testable with a
// fake wallet (see wallet_test.go).
func sendGroupPayments(milieu *core.Milieu, sender WalletSender, redisClient redisFlagSetter, walletRPCTimeout time.Duration, group PayoutGroup, batchID int) (*tari_generated.TransferResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), walletRPCTimeout)
	defer cancel()

	resp, err := sender.SendTransactions(ctx, group.Payments, !group.IsIndividual)
	if err == nil {
		return resp, nil
	}

	if errors.Is(err, walletGRPC.ErrAmbiguousBroadcast) {
		groupLabel := fmt.Sprintf("tier %v individual", group.Tier)
		if !group.IsIndividual {
			groupLabel = fmt.Sprintf("tier %v batch %v", group.Tier, group.BatchIndex)
		}
		milieu.Error(fmt.Sprintf(
			"AMBIGUOUS BROADCAST: wallet Transfer RPC failed with NO signal on whether the "+
				"transaction actually broadcast before failing, for batch ID %v (%v). Flagging "+
				"%v recipient(s) for manual reconciliation -- do NOT retry these balances until a "+
				"human confirms via the wallet's transaction history whether this money already "+
				"went out. Underlying error: %v",
			batchID, groupLabel, len(group.Payments), err))
		milieu.CaptureException(err)
		for i, payment := range group.Payments {
			milieu.Error(fmt.Sprintf(
				"Ambiguous-broadcast recipient: batch %v, %v, index %v, address %v, amount %v",
				batchID, groupLabel, i, payment.Address, payment.Amount))
		}
		if flagErr := flagForReconciliation(redisClient, group.Payments); flagErr != nil {
			milieu.CaptureException(flagErr)
			milieu.Error(flagErr.Error())
		}
	}

	return resp, err
}

// listReconcilePending scans Redis for every key matching reconcilePendingKeyPattern and logs
// the address it was raised for plus the value stored for it (the UTC timestamp
// flagForReconciliation recorded when the flag was set, if the key still holds the value this
// tool wrote -- an operator may also have hand-edited it, so the raw value is printed as-is
// rather than assumed to always parse as a timestamp).
//
// Uses SCAN (not KEYS) to walk the keyspace, since this may run against a production Redis
// instance shared with other traffic and a blocking KEYS * call would stall it.
func listReconcilePending(milieu *core.Milieu) {
	ctx := context.Background()
	redisClient := milieu.GetRedis()

	milieu.Info("Listing every address currently flagged for ambiguous-broadcast reconciliation:")
	var cursor uint64
	found := 0
	for {
		keys, nextCursor, err := redisClient.Scan(ctx, cursor, reconcilePendingKeyPattern, 100).Result()
		if err != nil {
			milieu.CaptureException(err)
			milieu.Error(err.Error())
			return
		}
		for _, key := range keys {
			address := strings.TrimPrefix(key, reconcilePendingKeyPrefix)
			val, err := redisClient.Get(ctx, key).Result()
			if err != nil {
				milieu.Info(fmt.Sprintf("%v (flagged, but failed to read value: %v)", address, err))
				found++
				continue
			}
			milieu.Info(fmt.Sprintf("%v (flagged at %v)", address, val))
			found++
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	milieu.Info(fmt.Sprintf("%v address(es) currently flagged for reconciliation", found))
}
