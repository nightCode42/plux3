// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command manifestload measures the manifest endpoint's throughput
// (NFR-020) with less overhead than a scripted load tool, so a small
// machine can drive one api replica to its limit.
//
// It registers -devices devices of an app's environment, syncs each
// once, then sends every device's "anything new?" request — the one
// devices make on each app start, carrying the ETag they hold — at a
// constant -rate for -duration, and reports throughput and latency
// percentiles. Every device sends its own X-Forwarded-For, so the server
// must list the sender in server.trustedProxies for per-address rate
// limits to apply per device, as they would in the field.
//
//	manifestload -server http://localhost:8080 -app <id> -env staging -rate 5000
//
// The run fails (exit 1) when the p99 latency exceeds -p99 or any
// request fails. Test/load/manifest.js is the same test for k6.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"
)

// device is one simulated device's prepared request.
type device struct {
	header http.Header
	body   []byte
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("manifestload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", "http://localhost:8080", "the server's base `url`")
	app := fs.String("app", "", "the app `id`")
	env := fs.String("env", "staging", "the environment `key`")
	devices := fs.Int("devices", 3000, "simulated devices; each may make api.requestsPerMinutePerDevice calls a minute")
	rate := fs.Int("rate", 5000, "requests per second")
	duration := fs.Duration("duration", time.Minute, "how long to send")
	p99 := fs.Duration("p99", 50*time.Millisecond, "the p99 latency the run must meet")
	if err := fs.Parse(args); err != nil || *app == "" || *devices < 1 || *rate < 1 {
		_, _ = fmt.Fprintln(stderr, "usage: manifestload -app id [-server url] [-env key] [-devices n] [-rate n] [-duration d] [-p99 d]")
		return 2
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 2000, MaxConnsPerHost: 2000}}
	devs, err := prepare(client, *server, *app, *env, *devices)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "manifestload:", err)
		return 1
	}
	r := load(client, *server, devs, *rate, *duration)
	_, _ = fmt.Fprintf(stdout, "requests %d in %.1fs (%.0f/s), failures %d, p50 %v, p95 %v, p99 %v, max %v\n",
		r.count, r.elapsed.Seconds(), float64(r.count)/r.elapsed.Seconds(), r.failures, r.p(0.50), r.p(0.95), r.p(0.99), r.p(1))
	for reason, n := range r.reasons {
		_, _ = fmt.Fprintf(stdout, "  %d × %s\n", n, reason)
	}
	if r.failures > 0 || r.p(0.99) > *p99 {
		return 1
	}
	return 0
}

// call posts a Connect JSON request and decodes the reply.
func call(c *http.Client, url string, h http.Header, body any) (map[string]any, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err //nolint:wrapcheck // reported as is
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b)) //nolint:noctx // a command-line tool
	if err != nil {
		return nil, err //nolint:wrapcheck // reported as is
	}
	req.Header = h.Clone()
	res, err := c.Do(req)
	if err != nil {
		return nil, err //nolint:wrapcheck // reported as is
	}
	defer func() { _ = res.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %d %v", url, res.StatusCode, out)
	}
	return out, nil
}

// prepare registers the devices, syncs each once and builds the request
// each will repeat.
func prepare(c *http.Client, server, app, env string, n int) ([]device, error) {
	devs := make([]device, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for i := range devs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			devs[i], errs[i] = prepareOne(c, server, app, env, i+1)
		}(i)
	}
	wg.Wait()
	return devs, errors.Join(errs...)
}

func prepareOne(c *http.Client, server, app, env string, i int) (device, error) {
	h := http.Header{"Content-Type": {"application/json"}, "X-Forwarded-For": {fmt.Sprintf("10.%d.%d.%d", (i>>16)&255, (i>>8)&255, i&255)}}
	reg, err := call(c, server+"/plux.v1.DeviceService/RegisterDevice", h, map[string]any{"appId": app, "environment": env, "platform": "android", "runtimeVersion": "1.0.0"})
	if err != nil {
		return device{}, err
	}
	d, _ := reg["device"].(map[string]any)
	tok, err := call(c, server+"/plux.v1.TokenService/IssueDeviceToken", h, map[string]any{"deviceId": d["id"], "deviceSecret": reg["deviceSecret"]})
	if err != nil {
		return device{}, err
	}
	token, _ := tok["accessToken"].(string)
	h.Set("Authorization", "Bearer "+token)
	first, err := call(c, server+"/plux.v1.ManifestService/GetManifest", h, map[string]any{})
	if err != nil {
		return device{}, err
	}
	m, _ := first["manifest"].(map[string]any)
	appBundle, _ := m["appBundle"].(map[string]any)
	installed := []map[string]any{{"key": "", "sha256": appBundle["sha256"]}}
	plugins, _ := m["plugins"].([]any)
	for _, p := range plugins {
		pm, _ := p.(map[string]any)
		b, _ := pm["bundle"].(map[string]any)
		installed = append(installed, map[string]any{"key": pm["key"], "sha256": b["sha256"]})
	}
	body := map[string]any{"installedSequence": m["releaseSequence"], "installed": installed}
	synced, err := call(c, server+"/plux.v1.ManifestService/GetManifest", h, body)
	if err != nil {
		return device{}, err
	}
	body["ifNoneMatch"] = synced["etag"]
	b, err := json.Marshal(body)
	if err != nil {
		return device{}, err //nolint:wrapcheck // reported as is
	}
	return device{header: h, body: b}, nil
}

// result is a run's outcome.
type result struct {
	count, failures int
	elapsed         time.Duration
	latencies       []time.Duration
	reasons         map[string]int
}

// p is a latency percentile; p(1) is the maximum.
func (r result) p(q float64) time.Duration {
	if len(r.latencies) == 0 {
		return 0
	}
	return r.latencies[int(q*float64(len(r.latencies)-1))]
}

// load sends the devices' requests at a constant rate from 16 pacers.
func load(c *http.Client, server string, devs []device, rate int, d time.Duration) result {
	const pacers = 16
	url := server + "/plux.v1.ManifestService/GetManifest"
	var (
		mu  sync.Mutex
		r   = result{reasons: map[string]int{}}
		all sync.WaitGroup
	)
	start := time.Now()
	end := start.Add(d)
	interval := time.Second * pacers / time.Duration(rate)
	for w := range pacers {
		all.Add(1)
		go func() {
			defer all.Done()
			next := time.Now()
			for i := w; time.Now().Before(end); i += pacers {
				next = next.Add(interval)
				if wait := time.Until(next); wait > 0 {
					time.Sleep(wait)
				}
				dev := devs[i%len(devs)]
				all.Add(1)
				go func() {
					defer all.Done()
					took, reason := once(c, url, dev)
					mu.Lock()
					r.latencies = append(r.latencies, took)
					if reason != "" {
						r.failures++
						r.reasons[reason]++
					}
					mu.Unlock()
				}()
			}
		}()
	}
	all.Wait()
	r.elapsed = time.Since(start)
	r.count = len(r.latencies)
	slices.Sort(r.latencies)
	return r
}

// once sends one request; the reason is empty when the answer was "not
// modified".
func once(c *http.Client, url string, d device) (time.Duration, string) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(d.body)) //nolint:noctx // a command-line tool
	if err != nil {
		return 0, err.Error()
	}
	req.Header = d.header.Clone()
	start := time.Now()
	res, err := c.Do(req)
	if err != nil {
		return time.Since(start), "transport error"
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	took := time.Since(start)
	if res.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"notModified":true`)) {
		return took, fmt.Sprintf("status %d", res.StatusCode)
	}
	return took, ""
}
