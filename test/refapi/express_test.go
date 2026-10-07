// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

// productPage is the answer of the product list.
type productPage struct {
	Items []product
	Next  *string
	More  bool
}

func TestProductsPaginate(t *testing.T) {
	srv := testServer(t, Options{})
	var ids []string
	path := "/express/v1/products?limit=15"
	for page := 0; ; page++ {
		if page > 4 {
			t.Fatal("pagination does not end")
		}
		r := call(t, srv, http.MethodGet, path, "", "")
		if r.status != http.StatusOK {
			t.Fatalf("status %d: %s", r.status, r.body)
		}
		var p productPage
		r.json(t, &p)
		for _, it := range p.Items {
			ids = append(ids, it.ID)
		}
		if p.More != (p.Next != nil) {
			t.Fatalf("page %d: more=%v next=%v", page, p.More, p.Next)
		}
		if !p.More {
			break
		}
		path = "/express/v1/products?limit=15&cursor=" + url.QueryEscape(*p.Next)
	}
	if len(ids) != 40 || ids[0] != "p-001" || ids[39] != "p-040" {
		t.Fatalf("the pages held %d products: %v", len(ids), ids)
	}
}

func TestProductsDefaultPageSize(t *testing.T) {
	srv := testServer(t, Options{})
	var p productPage
	call(t, srv, http.MethodGet, "/express/v1/products", "", "").json(t, &p)
	if len(p.Items) != defaultProducts || !p.More || *p.Next != "p-010" {
		t.Fatalf("the first page: %d items, more=%v", len(p.Items), p.More)
	}
}

func TestProductsErrors(t *testing.T) {
	srv := testServer(t, Options{})
	tests := []struct{ name, query, code string }{
		{"a limit that is no number", "limit=ten", "bad_limit"},
		{"a limit of zero", "limit=0", "bad_limit"},
		{"a limit over the maximum", "limit=" + strconv.Itoa(maxProducts+1), "bad_limit"},
		{"an unknown cursor", "cursor=p-999", "bad_cursor"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := call(t, srv, http.MethodGet, "/express/v1/products?"+tc.query, "", "")
			if r.status != http.StatusBadRequest || r.errorCode(t) != tc.code {
				t.Fatalf("answered %d %s", r.status, r.body)
			}
		})
	}
}

func TestGetProduct(t *testing.T) {
	srv := testServer(t, Options{})
	tests := []struct {
		id     string
		status int
	}{
		{"p-001", http.StatusOK},
		{"p-040", http.StatusOK},
		{"p-041", http.StatusNotFound},
		{"nope", http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			r := call(t, srv, http.MethodGet, "/express/v1/products/"+tc.id, "", "")
			if r.status != tc.status {
				t.Fatalf("status %d, want %d", r.status, tc.status)
			}
			if tc.status == http.StatusOK {
				var p product
				r.json(t, &p)
				if p.ID != tc.id || p.PriceCents <= 0 {
					t.Fatalf("answered %+v", p)
				}
			}
		})
	}
}

func TestPlaceOrder(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
		code   string
		total  int64
	}{
		{"one product", `{"items":[{"productId":"p-001","quantity":2}]}`, http.StatusCreated, "", 2 * 199},
		{"two products", `{"items":[{"productId":"p-001","quantity":1},{"productId":"p-002","quantity":3}]}`, http.StatusCreated, "", 199 + 3*249},
		{"an empty order", `{"items":[]}`, http.StatusUnprocessableEntity, "empty_order", 0},
		{"an unknown product", `{"items":[{"productId":"p-999","quantity":1}]}`, http.StatusUnprocessableEntity, "unknown_product", 0},
		{"a zero quantity", `{"items":[{"productId":"p-001","quantity":0}]}`, http.StatusUnprocessableEntity, "invalid_quantity", 0},
		{"a quantity over the maximum", `{"items":[{"productId":"p-001","quantity":100}]}`, http.StatusUnprocessableEntity, "invalid_quantity", 0},
		{"a body that is no JSON", `{`, http.StatusBadRequest, "bad_request", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := testServer(t, Options{})
			r := call(t, srv, http.MethodPost, "/express/v1/orders", "", tc.body)
			if r.status != tc.status {
				t.Fatalf("status %d, want %d: %s", r.status, tc.status, r.body)
			}
			if tc.code != "" {
				if got := r.errorCode(t); got != tc.code {
					t.Fatalf("error code %q, want %q", got, tc.code)
				}
				return
			}
			var out struct {
				ID         string
				Status     string
				TotalCents int64
			}
			r.json(t, &out)
			if out.ID != "ord-001" || out.Status != "placed" || out.TotalCents != tc.total {
				t.Fatalf("answered %+v", out)
			}
		})
	}
}

func TestCourierJobs(t *testing.T) {
	srv := testServer(t, Options{})
	var out struct{ Items []job }
	call(t, srv, http.MethodGet, "/express/v1/courier/jobs", "", "").json(t, &out)
	if len(out.Items) != 3 || out.Items[0].ID != "job-001" || out.Items[0].Status != "assigned" {
		t.Fatalf("jobs: %+v", out.Items)
	}
}

func TestConfirmDelivery(t *testing.T) {
	srv := testServer(t, Options{})
	post := func(job, key string) reply {
		var headers []string
		if key != "" {
			headers = []string{"Idempotency-Key", key}
		}
		return call(t, srv, http.MethodPost, "/express/v1/courier/jobs/"+job+"/deliveries", "", `{"note":"left at the door"}`, headers...)
	}

	if r := post("job-001", ""); r.status != http.StatusBadRequest || r.errorCode(t) != "idempotency_key_required" {
		t.Fatalf("without a key: %d %s", r.status, r.body)
	}
	if r := post("job-999", "k0"); r.status != http.StatusNotFound {
		t.Fatalf("an unknown job: %d", r.status)
	}
	first := post("job-001", "k1")
	if first.status != http.StatusCreated {
		t.Fatalf("the first confirmation: %d %s", first.status, first.body)
	}
	// The outbox replays with the key it made.
	replay := post("job-001", "k1")
	if replay.status != http.StatusCreated || string(replay.body) != string(first.body) {
		t.Fatalf("the replay: %d %s", replay.status, replay.body)
	}
	// Another key for a job already delivered is a conflict.
	if r := post("job-001", "k2"); r.status != http.StatusConflict || r.errorCode(t) != "already_delivered" {
		t.Fatalf("a second delivery: %d %s", r.status, r.body)
	}

	var applied struct{ Items []delivery }
	call(t, srv, http.MethodGet, "/express/v1/courier/jobs/job-001/deliveries", "", "").json(t, &applied)
	if len(applied.Items) != 1 || applied.Items[0].Note != "left at the door" {
		t.Fatalf("the deliveries applied: %+v", applied.Items)
	}
	var jobs struct{ Items []job }
	call(t, srv, http.MethodGet, "/express/v1/courier/jobs", "", "").json(t, &jobs)
	if jobs.Items[0].Status != "delivered" || jobs.Items[1].Status != "assigned" {
		t.Fatalf("job statuses: %+v", jobs.Items)
	}
}
