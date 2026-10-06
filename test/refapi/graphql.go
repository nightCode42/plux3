// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// This is not a GraphQL engine. It answers the two queries the Plux Bank
// fixture sends and nothing else: the first field of the query's selection
// names the query (`accounts` or `transactions`), its variables carry the
// arguments, and the answer holds every field of the object whatever the
// selection asks for. A query with another first field, a mutation or a
// subscription is answered with a GraphQL error.

// queryField finds the first field of a query document's selection.
var queryField = regexp.MustCompile(`(?s)^\s*(?:query\b[^{(]*(?:\([^)]*\))?\s*)?\{\s*([A-Za-z_][A-Za-z0-9_]*)`)

// Page sizes of the transactions query.
const (
	defaultPage = 10
	maxPage     = 50
)

// graphqlRequest is the body of a GraphQL POST.
type graphqlRequest struct {
	Query     string          `json:"query"`
	Variables json.RawMessage `json:"variables"`
}

// graphqlError is an entry of the errors array.
type graphqlError struct {
	Message string `json:"message"`
}

// graphql answers a query; GraphQL errors are a 200 with an errors array.
func (a *api) graphql(w http.ResponseWriter, r *http.Request) {
	var in graphqlRequest
	if !readJSON(w, r, &in) {
		return
	}
	fail := func(msg string) {
		writeJSON(w, http.StatusOK, map[string]any{"data": nil, "errors": []graphqlError{{Message: msg}}})
	}
	m := queryField.FindStringSubmatch(in.Query)
	if m == nil {
		fail("only queries are supported")
		return
	}
	switch m[1] {
	case "accounts":
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"accounts": a.accountsSnapshot()}})
	case "transactions":
		var vars struct {
			AccountID string  `json:"accountId"`
			First     *int    `json:"first"`
			After     *string `json:"after"`
		}
		if len(in.Variables) > 0 && json.Unmarshal(in.Variables, &vars) != nil {
			fail("the variables are malformed")
			return
		}
		page, err := a.transactionsPage(vars.AccountID, vars.First, vars.After)
		if err != "" {
			fail(err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"transactions": page}})
	default:
		fail("the query " + m[1] + " is not supported")
	}
}

// transactionsPage is a page of an account's bookings.
type transactionsPage struct {
	Items []transaction `json:"items"`
	Next  *string       `json:"next"`
	More  bool          `json:"more"`
}

// transactionsPage returns up to first bookings of an account after the one
// with ID after, newest first; the error is a message for the GraphQL
// errors array, empty when there is none.
func (a *api) transactionsPage(accountID string, first *int, after *string) (page transactionsPage, problem string) {
	size := defaultPage
	if first != nil {
		size = *first
	}
	if size < 1 || size > maxPage {
		return page, "first must be between 1 and 50"
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	d := &a.store.bank
	if d.account(accountID) == nil {
		return page, "the account does not exist"
	}
	all := d.transactions[accountID]
	start := 0
	if after != nil && *after != "" {
		found := false
		for i, t := range all {
			if t.ID == *after {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return page, "the cursor is unknown"
		}
	}
	end := min(start+size, len(all))
	page.Items = append([]transaction{}, all[start:end]...)
	page.More = end < len(all)
	if page.More {
		last := page.Items[len(page.Items)-1].ID
		page.Next = &last
	}
	return page, ""
}
