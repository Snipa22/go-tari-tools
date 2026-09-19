package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
	"github.com/Snipa22/go-tari-lib/v3/walletGRPC"
	"log"
	"time"
)

func main() {
	walletAddress := flag.String("walletAddress", "", "Wallet address to send to")
	amount := flag.Int("amount", 0, "Amount of uT to send, if you want to send 1XTM, this should be 1000000")
	walletGRPCAddressPtr := flag.String("wallet-grpc-address", "127.0.0.1:18143", "Tari wallet GRPC address")
	flag.Parse()
	client, err := walletGRPC.New(*walletGRPCAddressPtr)
	if err != nil {
		log.Fatalln(err)
	}
	if *walletAddress == "" {
		log.Fatalln("No valid wallet address passed")
	}
	fmt.Printf("Sending %v uT to %v\n", *amount, *walletAddress)
	txns := make([]*tari_generated.PaymentRecipient, 0)
	txns = append(txns, &tari_generated.PaymentRecipient{
		Address:     *walletAddress,
		Amount:      uint64(*amount),
		FeePerGram:  uint64(5),
		PaymentType: tari_generated.PaymentRecipient_ONE_SIDED,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.SendTransactions(ctx, txns, false)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Submitted request to wallet, txid: %v or error: %v\n", resp.Results[0].TransactionId, resp.Results[0].FailureMessage)
}
