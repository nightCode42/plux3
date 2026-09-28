// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package objects_test

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/storage/objects"
)

// stores returns each backend under test, with a name.
func stores(t *testing.T, cdn string) map[string]objects.Store {
	t.Helper()
	fs, err := objects.NewFilesystem(t.TempDir(), cdn)
	if err != nil {
		t.Fatalf("NewFilesystem: %v", err)
	}
	_, srv := newS3Stub()
	t.Cleanup(srv.Close)
	s3, err := objects.NewS3(context.Background(), objects.S3Options{
		Endpoint: srv.URL, Bucket: "plux", PathStyle: true,
		AccessKeyID: "test", SecretAccessKey: "test", CDNBaseURL: cdn,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	out := map[string]objects.Store{"filesystem": fs, "s3": s3}
	// A real S3-compatible store, when one is configured, runs every
	// test too (QA-005).
	if endpoint := os.Getenv("PLUX_TEST_S3_ENDPOINT"); endpoint != "" {
		real, err := objects.NewS3(context.Background(), objects.S3Options{
			Endpoint: endpoint, Bucket: os.Getenv("PLUX_TEST_S3_BUCKET"), PathStyle: true,
			AccessKeyID: os.Getenv("PLUX_TEST_S3_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("PLUX_TEST_S3_SECRET_ACCESS_KEY"),
			CDNBaseURL: cdn,
		})
		if err != nil {
			t.Fatalf("NewS3(%s): %v", endpoint, err)
		}
		out["s3-server"] = real
	}
	return out
}

// Verifies: SRV-023.
func TestKeysAreContentAddressed(t *testing.T) {
	t.Parallel()
	digest := objects.Digest([]byte("hello"))
	k, err := objects.Key(objects.KindBundle, digest)
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if want := "bundles/" + digest[:2] + "/" + digest; k != want {
		t.Errorf("Key = %q; want %q", k, want)
	}
	if objects.Digest([]byte("hello")) != digest {
		t.Error("Digest is not deterministic")
	}
	for _, tc := range []struct {
		kind   objects.Kind
		digest string
	}{
		{objects.KindBundle, "not-a-digest"},
		{objects.KindBundle, strings.ToUpper(digest)},
		{"secrets", digest},
	} {
		if got, err := objects.Key(tc.kind, tc.digest); err == nil {
			t.Errorf("Key(%q, %q) = %q; want an error", tc.kind, tc.digest, got)
		}
	}
}

// Verifies: SRV-023, REL-024.
func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()
	for name, store := range stores(t, "") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			data := []byte("the quick brown fox")
			key, err := objects.Key(objects.KindAsset, objects.Digest(data))
			if err != nil {
				t.Fatal(err)
			}
			info, err := store.Put(ctx, key, data, "text/plain")
			if err != nil {
				t.Fatalf("Put: %v", err)
			}
			if info.Size != int64(len(data)) {
				t.Errorf("Put size %d", info.Size)
			}
			// Storing the same content again is a no-op.
			if _, err := store.Put(ctx, key, data, "text/plain"); err != nil {
				t.Errorf("second Put: %v", err)
			}
			got, info, err := store.Get(ctx, key)
			if err != nil || string(got) != string(data) {
				t.Fatalf("Get = %q, %v", got, err)
			}
			if info.Size != int64(len(data)) {
				t.Errorf("Get size %d", info.Size)
			}
			if st, err := store.Stat(ctx, key); err != nil || st.Size != int64(len(data)) {
				t.Errorf("Stat = %+v, %v", st, err)
			}
			if err := store.Delete(ctx, key); err != nil {
				t.Errorf("Delete: %v", err)
			}
			if err := store.Delete(ctx, key); err != nil {
				t.Errorf("deleting twice must succeed: %v", err)
			}
			if _, err := store.Stat(ctx, key); !errors.Is(err, objects.ErrNotFound) {
				t.Errorf("Stat after Delete = %v; want ErrNotFound", err)
			}
			if _, _, err := store.Get(ctx, key); !errors.Is(err, objects.ErrNotFound) {
				t.Errorf("Get after Delete = %v; want ErrNotFound", err)
			}
		})
	}
}

// Verifies: REL-024.
func TestStoreServesRanges(t *testing.T) {
	t.Parallel()
	for name, store := range stores(t, "") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			data := []byte("0123456789")
			key, err := objects.Key(objects.KindDelta, objects.Digest(data))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Put(ctx, key, data, "application/octet-stream"); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				offset, n int64
				want      string
			}{{0, -1, "0123456789"}, {3, 4, "3456"}, {5, -1, "56789"}, {0, 1, "0"}} {
				r, info, err := store.Open(ctx, key, tc.offset, tc.n)
				if err != nil {
					t.Fatalf("Open(%d, %d): %v", tc.offset, tc.n, err)
				}
				got, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil || string(got) != tc.want {
					t.Errorf("Open(%d, %d) = %q, %v; want %q", tc.offset, tc.n, got, err, tc.want)
				}
				if info.Size != int64(len(tc.want)) {
					t.Errorf("Open(%d, %d) size %d; want %d", tc.offset, tc.n, info.Size, len(tc.want))
				}
			}
		})
	}
}

// Verifies: SRV-023, DEP-041.
func TestURLPrefersTheCDN(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	data := []byte("bundle")
	key, err := objects.Key(objects.KindBundle, objects.Digest(data))
	if err != nil {
		t.Fatal(err)
	}
	for name, store := range stores(t, "https://cdn.example") {
		if _, err := store.Put(ctx, key, data, ""); err != nil {
			t.Fatal(err)
		}
		got, err := store.URL(ctx, key, time.Minute)
		if err != nil || got != "https://cdn.example/"+key {
			t.Errorf("%s: URL = %q, %v", name, got, err)
		}
	}
	// Without a CDN the filesystem backend has no URL, so the server
	// serves the bytes; S3 signs one.
	for name, store := range stores(t, "") {
		if _, err := store.Put(ctx, key, data, ""); err != nil {
			t.Fatal(err)
		}
		got, err := store.URL(ctx, key, time.Minute)
		if err != nil {
			t.Fatalf("%s: URL: %v", name, err)
		}
		switch name {
		case "filesystem":
			if got != "" {
				t.Errorf("filesystem URL = %q; want the server to serve the bytes", got)
			}
		case "s3":
			if !strings.Contains(got, key) || !strings.Contains(got, "X-Amz-Signature") {
				t.Errorf("s3 URL = %q; want a signed URL", got)
			}
		}
	}
}

// Verifies: REL-024.
// Stored objects are immutable, so they carry the cache headers a CDN
// needs to keep them for ever.
func TestS3StoresImmutableCacheHeaders(t *testing.T) {
	t.Parallel()
	stub, srv := newS3Stub()
	defer srv.Close()
	store, err := objects.NewS3(context.Background(), objects.S3Options{
		Endpoint: srv.URL, Bucket: "plux", PathStyle: true,
		AccessKeyID: "test", SecretAccessKey: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("immutable")
	key, err := objects.Key(objects.KindBundle, objects.Digest(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), key, data, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if got := stub.cacheControl(key); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if stub.count() != 1 {
		t.Errorf("%d objects stored", stub.count())
	}
}

func TestFilesystemRefusesEscapingKeys(t *testing.T) {
	t.Parallel()
	store, err := objects.NewFilesystem(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, key := range []string{"../escape", "a/../../escape", "/"} {
		if _, err := store.Put(ctx, key, []byte("x"), ""); err == nil {
			t.Errorf("Put(%q) was accepted", key)
		}
	}
	if _, err := objects.NewFilesystem("", ""); err == nil {
		t.Error("an empty directory was accepted")
	}
}

func TestOpenRefusesABadRange(t *testing.T) {
	t.Parallel()
	store, err := objects.NewFilesystem(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	data := []byte("12345")
	key, err := objects.Key(objects.KindExport, objects.Digest(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, key, data, ""); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int64{-1, 6} {
		if _, _, err := store.Open(ctx, key, offset, 1); err == nil {
			t.Errorf("Open at offset %d was accepted", offset)
		}
	}
	if _, _, err := store.Open(ctx, "bundles/aa/missing", 0, -1); !errors.Is(err, objects.ErrNotFound) {
		t.Errorf("Open of a missing key = %v", err)
	}
}

func TestNewS3NeedsABucket(t *testing.T) {
	t.Parallel()
	if _, err := objects.NewS3(context.Background(), objects.S3Options{}); err == nil {
		t.Error("a missing bucket was accepted")
	}
}
