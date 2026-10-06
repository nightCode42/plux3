// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// defaultHeartbeat is how often an idle notification stream sends a comment.
const defaultHeartbeat = 15 * time.Second

// notifications streams the bank's transfer events as server-sent events. A
// client that reconnects with Last-Event-ID receives the events it missed; a
// new one receives only what happens after it connects.
func (a *api) notifications(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "no_streaming", "the connection cannot stream")
		return
	}
	heartbeat := a.opts.Heartbeat
	if heartbeat <= 0 {
		heartbeat = defaultHeartbeat
	}
	a.store.mu.Lock()
	sent := len(a.store.bank.events)
	if last, err := strconv.Atoi(r.Header.Get("Last-Event-ID")); err == nil && last >= 0 && last <= sent {
		sent = last
	}
	a.store.mu.Unlock()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	for {
		a.store.mu.Lock()
		pending := append([]notification(nil), a.store.bank.events[sent:]...)
		if a.store.bank.signal == nil {
			a.store.bank.signal = make(chan struct{})
		}
		signal := a.store.bank.signal
		a.store.mu.Unlock()

		for _, n := range pending {
			data, err := json.Marshal(n)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: transfer\ndata: %s\n\n", n.ID, data); err != nil {
				return
			}
			flusher.Flush()
			sent = n.ID
		}
		select {
		case <-signal:
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
