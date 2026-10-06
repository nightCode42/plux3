// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// defaultTrackStep is the pause between the status steps of a tracking
// stream.
const defaultTrackStep = 2 * time.Second

// trackStep is one status of an order's delivery.
type trackStep struct {
	status     string
	lat, lng   float64
	etaMinutes int
}

// trackSteps are the statuses an order passes through, from the shop to the
// customer.
var trackSteps = []trackStep{
	{"placed", 52.5200, 13.4050, 20},
	{"picked_up", 52.5230, 13.4080, 15},
	{"on_the_way", 52.5265, 13.4115, 5},
	{"delivered", 52.5300, 13.4150, 0},
}

// trackMessage is what the stream sends at each step.
type trackMessage struct {
	OrderID    string  `json:"orderId"`
	Status     string  `json:"status"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	ETAMinutes int     `json:"etaMinutes"`
}

// track upgrades to a WebSocket and sends the order's status at each step,
// one every TrackStep, then closes normally.
func (a *api) track(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.store.mu.Lock()
	_, known := a.store.express.orders[id]
	a.store.mu.Unlock()
	if !known {
		writeError(w, http.StatusNotFound, "not_found", "there is no such order")
		return
	}
	step := a.opts.TrackStep
	if step <= 0 {
		step = defaultTrackStep
	}
	conn, err := upgrade(w, r)
	if err != nil {
		return
	}
	defer func() { _ = conn.shut() }()

	// The client's frames are read to answer its pings and to notice it
	// leaving.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		for {
			if _, _, err := conn.readMessage(); err != nil {
				return
			}
		}
	}()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for _, s := range trackSteps {
		select {
		case <-timer.C:
		case <-gone:
			return
		}
		msg, err := json.Marshal(trackMessage{OrderID: id, Status: s.status, Lat: s.lat, Lng: s.lng, ETAMinutes: s.etaMinutes})
		if err != nil || conn.writeText(msg) != nil {
			return
		}
		timer.Reset(step)
	}
	if conn.close(closeNormal) != nil {
		return
	}
	// Give the client the time to answer the close before the connection
	// drops.
	select {
	case <-gone:
	case <-time.After(2 * time.Second):
	}
}
