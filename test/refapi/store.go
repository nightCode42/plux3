// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// store is the reference API's state. Every request goes through its mutex;
// it is seeded the same way on every start, so the e2e flows can name the
// accounts, products and jobs they use. Amounts are integer cents.
type store struct {
	mu      sync.Mutex
	bank    bankData
	express expressData
}

// newStore returns a store with the seed data.
func newStore() *store {
	return &store{bank: seedBank(), express: seedExpress()}
}

// epoch is the instant the seed data is dated from.
var epoch = time.Date(2026, time.January, 15, 9, 0, 0, 0, time.UTC)

// newToken returns a random bearer token.
func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating a token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// account is a bank account.
type account struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	IBAN         string `json:"iban"`
	BalanceCents int64  `json:"balanceCents"`
	Currency     string `json:"currency"`
}

// transaction is a booking on an account; debits are negative.
type transaction struct {
	ID           string `json:"id"`
	AccountID    string `json:"accountId"`
	Description  string `json:"description"`
	AmountCents  int64  `json:"amountCents"`
	PostedAt     string `json:"postedAt"`
	BalanceCents int64  `json:"balanceCents"`
}

// transferResult is what a completed transfer answers, kept for replays of
// its idempotency key.
type transferResult struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	BalanceCents int64  `json:"balanceCents"`
}

// notification is a server-sent event about a completed transfer.
type notification struct {
	ID           int    `json:"-"`
	TransferID   string `json:"transferId"`
	AccountID    string `json:"accountId"`
	ToIBAN       string `json:"toIban"`
	AmountCents  int64  `json:"amountCents"`
	BalanceCents int64  `json:"balanceCents"`
}

// bankData is the bank's state.
type bankData struct {
	tokens       map[string]struct{}
	accounts     []*account
	transactions map[string][]transaction // by account, newest first
	transfers    map[string]transferResult
	transferSeq  int
	events       []notification
	signal       chan struct{} // closed when an event is appended
}

// seedBank returns the bank's seed: two accounts and a month of bookings on
// the first.
func seedBank() bankData {
	d := bankData{
		tokens: map[string]struct{}{},
		accounts: []*account{
			{ID: "acct-001", Name: "Checking", IBAN: "DE89370400440532013000", BalanceCents: 250_000, Currency: "EUR"},
			{ID: "acct-002", Name: "Savings", IBAN: "DE02120300000000202051", BalanceCents: 1_200_000, Currency: "EUR"},
		},
		transactions: map[string][]transaction{},
		transfers:    map[string]transferResult{},
	}
	descriptions := []string{"Groceries", "Rent", "Salary", "Coffee", "Transit pass"}
	amounts := []int64{-4_250, -85_000, 320_000, -380, -8_600}
	const seeded = 25
	balance := d.accounts[0].BalanceCents
	for i := range seeded {
		// Newest first: booking 1 is the latest.
		n := i % len(descriptions)
		d.transactions["acct-001"] = append(d.transactions["acct-001"], transaction{
			ID:           fmt.Sprintf("txn-%03d", i+1),
			AccountID:    "acct-001",
			Description:  descriptions[n],
			AmountCents:  amounts[n],
			PostedAt:     epoch.Add(-time.Duration(i) * 24 * time.Hour).Format(time.RFC3339),
			BalanceCents: balance,
		})
		balance -= amounts[n]
	}
	return d
}

// product is something the express shop sells.
type product struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PriceCents int64  `json:"priceCents"`
	Category   string `json:"category"`
}

// orderLine is one line of an order.
type orderLine struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

// order is a placed order.
type order struct {
	ID         string
	Lines      []orderLine
	TotalCents int64
}

// job is a courier's delivery job.
type job struct {
	ID      string `json:"id"`
	OrderID string `json:"orderId"`
	Address string `json:"address"`
	Status  string `json:"status"`
}

// delivery is a confirmed delivery.
type delivery struct {
	ID    string `json:"id"`
	JobID string `json:"jobId"`
	Note  string `json:"note"`
	Key   string `json:"key"`
}

// expressData is the express shop's state.
type expressData struct {
	products   []product
	orders     map[string]*order
	orderSeq   int
	jobs       []*job
	deliveries []delivery
	byKey      map[string]delivery // by idempotency key
}

// seedExpress returns the shop's seed: 40 products and three courier jobs.
func seedExpress() expressData {
	categories := []string{"Fruit", "Bakery", "Dairy", "Drinks"}
	d := expressData{orders: map[string]*order{}, byKey: map[string]delivery{}}
	for i := range 40 {
		d.products = append(d.products, product{
			ID:         fmt.Sprintf("p-%03d", i+1),
			Name:       fmt.Sprintf("Product %02d", i+1),
			PriceCents: 199 + int64(i%7)*50,
			Category:   categories[i%len(categories)],
		})
	}
	for i := range 3 {
		d.jobs = append(d.jobs, &job{
			ID:      fmt.Sprintf("job-%03d", i+1),
			OrderID: fmt.Sprintf("ord-%03d", 900+i),
			Address: fmt.Sprintf("%d Harbour Street", 10+i),
			Status:  "assigned",
		})
	}
	return d
}
