// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// keyringService names the CLI's entries in the OS credential store.
const keyringService = "plux"

// tokenEnv overrides any stored token; CI uses it (SRV-064).
const tokenEnv = "PLUX_TOKEN"

// credentials keeps the CLI's token per server: in the OS credential
// store, or where none is reachable in a user-only file (ADR-0028).
type credentials struct {
	// dir is the CLI's configuration directory.
	dir string
}

// where names the place a token was stored.
const (
	inKeychain = "keychain"
	inFile     = "file"
)

func (c credentials) file() string { return filepath.Join(c.dir, "credentials.json") }

// save stores a token for a server and reports where.
func (c credentials) save(server, token string) (string, error) {
	if err := keyring.Set(keyringService, server, token); err == nil {
		_ = c.forgetFile(server)
		return inKeychain, nil
	}
	all, err := c.readFile()
	if err != nil {
		return "", err
	}
	all[server] = token
	if err := c.writeFile(all); err != nil {
		return "", err
	}
	return inFile, nil
}

// load returns the token for a server: PLUX_TOKEN, the credential store,
// then the fallback file; "" when there is none.
func (c credentials) load(server string) (string, error) {
	if t := os.Getenv(tokenEnv); t != "" {
		return t, nil
	}
	t, err := keyring.Get(keyringService, server)
	if err == nil {
		return t, nil
	}
	all, ferr := c.readFile()
	if ferr != nil {
		return "", ferr
	}
	return all[server], nil
}

// remove deletes a server's token wherever it is.
func (c credentials) remove(server string) error {
	// A missing entry or a machine without a store are both fine: the
	// file is then all there is.
	_ = keyring.Delete(keyringService, server)
	return c.forgetFile(server)
}

func (c credentials) forgetFile(server string) error {
	all, err := c.readFile()
	if err != nil || all[server] == "" {
		return err
	}
	delete(all, server)
	return c.writeFile(all)
}

func (c credentials) readFile() (map[string]string, error) {
	data, err := os.ReadFile(c.file())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	all := map[string]string{}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("read %s: %w", c.file(), err)
	}
	return all, nil
}

func (c credentials) writeFile(all map[string]string) error {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	data, err := json.Marshal(all)
	if err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	tmp, err := os.CreateTemp(c.dir, ".credentials-*")
	if err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Chmod(0o600), tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := os.Rename(tmp.Name(), c.file()); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write credentials: %w", err)
	}
	return nil
}
