// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The reference user: constant test credentials, never real ones.
const (
	demoUsername = "demo"
	demoPassword = "demo1234"
	tokenTTL     = time.Hour
)

// ibanShape is the shape of an IBAN: two letters, two check digits and up
// to 30 alphanumerics. The check digits are not verified.
var ibanShape = regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}$`)

// authed answers 401 unless the request carries a token a login issued.
func (a *api) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearer(r)
		if ok {
			a.store.mu.Lock()
			_, ok = a.store.bank.tokens[token]
			a.store.mu.Unlock()
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorised", "sign in to continue")
			return
		}
		next(w, r)
	}
}

// login issues a token for the demo user.
func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	user := subtle.ConstantTimeCompare([]byte(in.Username), []byte(demoUsername))
	pass := subtle.ConstantTimeCompare([]byte(in.Password), []byte(demoPassword))
	if user&pass != 1 {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "the username or password is wrong")
		return
	}
	token, err := newToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "a token could not be issued")
		return
	}
	a.store.mu.Lock()
	a.store.bank.tokens[token] = struct{}{}
	a.store.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expiresIn": int(tokenTTL.Seconds())})
}

// listAccounts lists the accounts.
func (a *api) listAccounts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": a.accountsSnapshot()})
}

// accountsSnapshot copies the accounts.
func (a *api) accountsSnapshot() []account {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	out := make([]account, 0, len(a.store.bank.accounts))
	for _, acc := range a.store.bank.accounts {
		out = append(out, *acc)
	}
	return out
}

// transferRequest is the body of a transfer.
type transferRequest struct {
	FromAccountID string `json:"fromAccountId"`
	ToIBAN        string `json:"toIban"`
	AmountCents   int64  `json:"amountCents"`
	Reference     string `json:"reference"`
}

// createTransfer moves money out of an account. A request that repeats an
// Idempotency-Key answers what the first one did, without moving money
// again.
func (a *api) createTransfer(w http.ResponseWriter, r *http.Request) {
	var in transferRequest
	if !readJSON(w, r, &in) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	d := &a.store.bank
	if prior, ok := d.transfers[key]; ok && key != "" {
		writeJSON(w, http.StatusCreated, prior)
		return
	}
	from := d.account(in.FromAccountID)
	iban := strings.ReplaceAll(strings.ToUpper(in.ToIBAN), " ", "")
	switch {
	case from == nil:
		writeError(w, http.StatusUnprocessableEntity, "unknown_account", "the account does not exist")
		return
	case !ibanShape.MatchString(iban):
		writeError(w, http.StatusUnprocessableEntity, "invalid_iban", "the recipient's IBAN is malformed")
		return
	case in.AmountCents <= 0:
		writeError(w, http.StatusUnprocessableEntity, "invalid_amount", "the amount must be positive")
		return
	case in.AmountCents > from.BalanceCents:
		writeError(w, http.StatusUnprocessableEntity, "insufficient_funds", "the amount is over the balance")
		return
	}
	d.transferSeq++
	id := fmt.Sprintf("tr-%04d", d.transferSeq)
	// Bookings after the seed are dated from it, a minute apart.
	at := epoch.Add(time.Duration(d.transferSeq) * time.Minute).Format(time.RFC3339)
	from.BalanceCents -= in.AmountCents
	d.book(from, transaction{ID: id, Description: orDefault(in.Reference, "Transfer"), AmountCents: -in.AmountCents, PostedAt: at})
	if to := d.accountByIBAN(iban); to != nil {
		to.BalanceCents += in.AmountCents
		d.book(to, transaction{ID: id + "-in", Description: orDefault(in.Reference, "Transfer"), AmountCents: in.AmountCents, PostedAt: at})
	}
	result := transferResult{ID: id, Status: "completed", BalanceCents: from.BalanceCents}
	if key != "" {
		d.transfers[key] = result
	}
	d.publish(notification{TransferID: id, AccountID: from.ID, ToIBAN: iban, AmountCents: in.AmountCents, BalanceCents: from.BalanceCents})
	writeJSON(w, http.StatusCreated, result)
}

// orDefault returns s, or fallback when s is blank.
func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// account finds an account by ID.
func (d *bankData) account(id string) *account {
	for _, a := range d.accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// accountByIBAN finds an account by IBAN.
func (d *bankData) accountByIBAN(iban string) *account {
	for _, a := range d.accounts {
		if a.IBAN == iban {
			return a
		}
	}
	return nil
}

// book records t on acc, newest first.
func (d *bankData) book(acc *account, t transaction) {
	t.AccountID, t.BalanceCents = acc.ID, acc.BalanceCents
	d.transactions[acc.ID] = append([]transaction{t}, d.transactions[acc.ID]...)
}

// publish appends a notification and wakes the streams waiting for one. The
// store's mutex is held.
func (d *bankData) publish(n notification) {
	n.ID = len(d.events) + 1
	d.events = append(d.events, n)
	if d.signal != nil {
		close(d.signal)
		d.signal = nil
	}
}
