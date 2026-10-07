// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/http"
	"strconv"
)

// Page sizes of the product list.
const (
	defaultProducts = 10
	maxProducts     = 50
)

// listProducts lists products a page at a time. The cursor is the ID of the
// last product of the previous page.
func (a *api) listProducts(w http.ResponseWriter, r *http.Request) {
	limit := defaultProducts
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxProducts {
			writeError(w, http.StatusBadRequest, "bad_limit", "limit must be a number from 1 to 50")
			return
		}
		limit = n
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	all := a.store.express.products
	start := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		found := false
		for i, p := range all {
			if p.ID == cursor {
				start, found = i+1, true
				break
			}
		}
		if !found {
			writeError(w, http.StatusBadRequest, "bad_cursor", "the cursor is unknown")
			return
		}
	}
	end := min(start+limit, len(all))
	body := struct {
		Items []product `json:"items"`
		Next  *string   `json:"next"`
		More  bool      `json:"more"`
	}{Items: append([]product{}, all[start:end]...), More: end < len(all)}
	if body.More {
		last := body.Items[len(body.Items)-1].ID
		body.Next = &last
	}
	writeJSON(w, http.StatusOK, body)
}

// getProduct returns one product.
func (a *api) getProduct(w http.ResponseWriter, r *http.Request) {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	for _, p := range a.store.express.products {
		if p.ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, p)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "there is no such product")
}

// placeOrder places an order for products.
func (a *api) placeOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items []orderLine `json:"items"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	d := &a.store.express
	if len(in.Items) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "empty_order", "an order needs at least one item")
		return
	}
	var total int64
	for _, line := range in.Items {
		p := d.product(line.ProductID)
		switch {
		case p == nil:
			writeError(w, http.StatusUnprocessableEntity, "unknown_product", "the product "+line.ProductID+" does not exist")
			return
		case line.Quantity < 1 || line.Quantity > 99:
			writeError(w, http.StatusUnprocessableEntity, "invalid_quantity", "the quantity must be from 1 to 99")
			return
		}
		total += p.PriceCents * int64(line.Quantity)
	}
	d.orderSeq++
	o := &order{ID: fmt.Sprintf("ord-%03d", d.orderSeq), Lines: in.Items, TotalCents: total}
	d.orders[o.ID] = o
	writeJSON(w, http.StatusCreated, map[string]any{"id": o.ID, "status": "placed", "totalCents": total})
}

// product finds a product by ID.
func (d *expressData) product(id string) *product {
	for i := range d.products {
		if d.products[i].ID == id {
			return &d.products[i]
		}
	}
	return nil
}

// listJobs lists the courier's jobs.
func (a *api) listJobs(w http.ResponseWriter, _ *http.Request) {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	items := make([]job, 0, len(a.store.express.jobs))
	for _, j := range a.store.express.jobs {
		items = append(items, *j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// confirmDelivery confirms a job's delivery. The Idempotency-Key header is
// required: the apps' outbox replays a delivery confirmed offline with the
// key it made, and a replay must not deliver twice.
func (a *api) confirmDelivery(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "send an Idempotency-Key header")
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	d := &a.store.express
	if prior, ok := d.byKey[key]; ok {
		writeJSON(w, http.StatusCreated, prior)
		return
	}
	var j *job
	for _, c := range d.jobs {
		if c.ID == r.PathValue("id") {
			j = c
		}
	}
	switch {
	case j == nil:
		writeError(w, http.StatusNotFound, "not_found", "there is no such job")
		return
	case j.Status == "delivered":
		writeError(w, http.StatusConflict, "already_delivered", "the job was delivered already")
		return
	}
	j.Status = "delivered"
	dl := delivery{ID: fmt.Sprintf("dlv-%03d", len(d.deliveries)+1), JobID: j.ID, Note: in.Note, Key: key}
	d.deliveries = append(d.deliveries, dl)
	d.byKey[key] = dl
	writeJSON(w, http.StatusCreated, dl)
}

// listDeliveries lists the deliveries applied to a job, which the e2e flows
// read to prove a replayed confirmation was applied once.
func (a *api) listDeliveries(w http.ResponseWriter, r *http.Request) {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	items := []delivery{}
	for _, dl := range a.store.express.deliveries {
		if dl.JobID == r.PathValue("id") {
			items = append(items, dl)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
