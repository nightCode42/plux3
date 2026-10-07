// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestTrackSendsEachStatusThenCloses(t *testing.T) {
	srv := testServer(t, Options{TrackStep: 5 * time.Millisecond})
	var placed struct{ ID string }
	call(t, srv, http.MethodPost, "/express/v1/orders", "", `{"items":[{"productId":"p-001","quantity":1}]}`).json(t, &placed)

	ws, res := dialWS(t, srv, "/express/v1/orders/"+placed.ID+"/track")
	if res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status %d", res.StatusCode)
	}
	if got := res.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept key %q (RFC 6455's example expects s3pPLMBiTxaQ9kYGzzhZRbK+xOo=)", got)
	}
	var statuses []string
	var last trackMessage
	for range trackSteps {
		op, payload := ws.recv(t)
		if op != opText {
			t.Fatalf("opcode %d, want text", op)
		}
		if err := json.Unmarshal(payload, &last); err != nil {
			t.Fatalf("a message is no JSON: %v", err)
		}
		if last.OrderID != placed.ID {
			t.Fatalf("a message of order %q", last.OrderID)
		}
		statuses = append(statuses, last.Status)
	}
	want := []string{"placed", "picked_up", "on_the_way", "delivered"}
	for i := range want {
		if statuses[i] != want[i] {
			t.Fatalf("statuses %v, want %v", statuses, want)
		}
	}
	if last.ETAMinutes != 0 || last.Lat == 0 || last.Lng == 0 {
		t.Fatalf("the last step: %+v", last)
	}

	op, payload := ws.recv(t)
	if op != opClose || closeCode(t, payload) != closeNormal {
		t.Fatalf("after the last status: opcode %d %x, want a normal close", op, payload)
	}
	ws.send(t, opClose, true, payload[:2])
}

func TestTrackWaitsOneStepBetweenStatuses(t *testing.T) {
	const step = 60 * time.Millisecond
	srv := testServer(t, Options{TrackStep: step})
	call(t, srv, http.MethodPost, "/express/v1/orders", "", `{"items":[{"productId":"p-001","quantity":1}]}`)
	ws, _ := dialWS(t, srv, "/express/v1/orders/ord-001/track")
	ws.recv(t)
	start := time.Now()
	ws.recv(t)
	if got := time.Since(start); got < step/2 {
		t.Fatalf("the second status came %v after the first, want about %v", got, step)
	}
}

func TestTrackAnswersPings(t *testing.T) {
	srv := testServer(t, Options{TrackStep: time.Minute})
	call(t, srv, http.MethodPost, "/express/v1/orders", "", `{"items":[{"productId":"p-001","quantity":1}]}`)
	ws, _ := dialWS(t, srv, "/express/v1/orders/ord-001/track")
	ws.recv(t)
	ws.send(t, opPing, true, []byte("are you there"))
	op, payload := ws.recv(t)
	if op != opPong || string(payload) != "are you there" {
		t.Fatalf("answered opcode %d %q", op, payload)
	}
}

func TestTrackRefusals(t *testing.T) {
	srv := testServer(t, Options{TrackStep: time.Millisecond})
	call(t, srv, http.MethodPost, "/express/v1/orders", "", `{"items":[{"productId":"p-001","quantity":1}]}`)
	t.Run("an unknown order", func(t *testing.T) {
		_, res := dialWS(t, srv, "/express/v1/orders/ord-999/track")
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("status %d", res.StatusCode)
		}
	})
	t.Run("a plain GET", func(t *testing.T) {
		r := call(t, srv, http.MethodGet, "/express/v1/orders/ord-001/track", "", "")
		if r.status != http.StatusBadRequest || r.errorCode(t) != "bad_handshake" {
			t.Fatalf("answered %d %s", r.status, r.body)
		}
	})
	t.Run("another WebSocket version", func(t *testing.T) {
		r := call(t, srv, http.MethodGet, "/express/v1/orders/ord-001/track", "", "",
			"Connection", "Upgrade", "Upgrade", "websocket", "Sec-WebSocket-Version", "8", "Sec-WebSocket-Key", handshakeKey)
		if r.status != http.StatusBadRequest || r.header.Get("Sec-WebSocket-Version") != "13" {
			t.Fatalf("answered %d %v", r.status, r.header)
		}
	})
}
