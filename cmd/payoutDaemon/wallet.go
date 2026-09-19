package main

import (
	"context"

	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
	"github.com/Snipa22/go-tari-lib/v3/walletGRPC"
)

// WalletSender is the narrow GRPC surface the payout-sending path depends on. It mirrors
// (*walletGRPC.Client).SendTransactions's exact signature (go-tari-lib v3) -- just behind an
// interface so tests can inject a fake wallet instead of dialing a live Tari wallet daemon, the
// same role go-tari-faucet/internal/faucet/wallet.go's WalletClient interface plays there.
//
// *walletGRPC.Client satisfies this structurally already (no wrapper struct needed, the
// signatures match exactly) -- see the compile-time assertion below.
type WalletSender interface {
	SendTransactions(ctx context.Context, transactions []*tari_generated.PaymentRecipient, singleTx bool) (*tari_generated.TransferResponse, error)
}

// var _ WalletSender = (*walletGRPC.Client)(nil) is a compile-time assertion that
// *walletGRPC.Client still satisfies WalletSender. If go-tari-lib ever changes
// (*Client).SendTransactions's signature, this line fails to compile immediately instead of the
// drift being discovered at a call site somewhere else in the codebase.
var _ WalletSender = (*walletGRPC.Client)(nil)
