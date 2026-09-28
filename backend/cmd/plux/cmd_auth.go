// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
)

// login signs in with the OAuth 2.0 device authorization grant (CLI-002):
// it prints a code and a URL, polls until a person approves in Studio,
// and stores the token (ADR-0028). It never reads from the terminal, so
// it cannot block CI; CI uses PLUX_TOKEN instead.
func (e env) login(args []string) int {
	var c common
	set := flag.NewFlagSet("login", flag.ContinueOnError)
	c.register(set, false)
	timeout := set.Duration("timeout", 10*time.Minute, "how long to wait for approval")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux login [--server url] [--json]\n\n"+
		"Signs in through the device authorization grant: approve the printed code in Studio.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("login", err)
	}
	cl, err := e.connect(c, false)
	if err != nil {
		return e.fail("login", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	start, err := cl.identity.StartDeviceAuthorization(ctx, connect.NewRequest(&pluxv1.StartDeviceAuthorizationRequest{Client: "plux-cli"}))
	if err != nil {
		return e.fail("login", err)
	}
	s := start.Msg
	_, _ = fmt.Fprintf(e.stderr, "Open %s and enter the code %s\n", firstOf(s.GetVerificationUriComplete(), s.GetVerificationUri()), s.GetUserCode())
	interval := time.Duration(max(s.GetIntervalSeconds(), 1)) * time.Second
	for {
		select {
		case <-ctx.Done():
			return e.fail("login", authError("the code was not approved in time"))
		case <-time.After(interval):
		}
		poll, err := cl.identity.PollDeviceAuthorization(ctx, connect.NewRequest(&pluxv1.PollDeviceAuthorizationRequest{DeviceCode: s.GetDeviceCode()}))
		if err != nil {
			return e.fail("login", err)
		}
		switch poll.Msg.GetStatus() {
		case "pending":
		case "slow_down":
			interval += 5 * time.Second
		case "approved":
			where, err := e.creds.save(c.server, poll.Msg.GetSecret())
			if err != nil {
				return e.fail("login", err)
			}
			if where == inFile {
				_, _ = fmt.Fprintf(e.stderr, "No OS credential store is available; the token is in %s, readable only by you.\n", e.creds.file())
			}
			return e.emit(c.json, map[string]string{"server": c.server, "stored": where}, "Signed in to "+c.server+".")
		default:
			return e.fail("login", authError("the request was "+poll.Msg.GetStatus()))
		}
	}
}

// logout forgets the stored token for a server.
func (e env) logout(args []string) int {
	var c common
	set := flag.NewFlagSet("logout", flag.ContinueOnError)
	c.register(set, false)
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux logout [--server url] [--json]\n\nForgets the stored token for the server.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("logout", err)
	}
	if err := e.creds.remove(c.server); err != nil {
		return e.fail("logout", err)
	}
	return e.emit(c.json, map[string]string{"server": c.server}, "Signed out of "+c.server+".")
}

// whoami shows who the token belongs to.
func (e env) whoami(args []string) int {
	var c common
	set := flag.NewFlagSet("whoami", flag.ContinueOnError)
	c.register(set, false)
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux whoami [--server url] [--org id] [--json]\n\nShows the signed-in user, their organisations and, with --org, their permissions.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("whoami", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("whoami", err)
	}
	res, err := cl.identity.GetCurrentUser(context.Background(), connect.NewRequest(&pluxv1.GetCurrentUserRequest{}))
	if err != nil {
		return e.fail("whoami", err)
	}
	m := res.Msg
	type membership struct {
		Organization string `json:"organization"`
		Role         string `json:"role"`
	}
	out := struct {
		ID          string       `json:"id"`
		Name        string       `json:"name"`
		Email       string       `json:"email"`
		Memberships []membership `json:"memberships"`
		Permissions []string     `json:"permissions"`
	}{ID: m.GetUser().GetId(), Name: m.GetUser().GetDisplayName(), Email: m.GetUser().GetEmail(), Memberships: []membership{}, Permissions: m.GetPermissions()}
	var text strings.Builder
	fmt.Fprintf(&text, "%s <%s>\n", out.Name, out.Email)
	for _, mb := range m.GetMemberships() {
		out.Memberships = append(out.Memberships, membership{Organization: mb.GetOrganizationId(), Role: mb.GetRole()})
		fmt.Fprintf(&text, "  %s  %s\n", mb.GetOrganizationId(), mb.GetRole())
	}
	return e.emit(c.json, out, text.String())
}
