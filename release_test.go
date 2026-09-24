package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelease_DeployRecordsDigest_RollbackReturnsPrior_ListRendersBoth(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}

	orig := imageDigest
	t.Cleanup(func() { imageDigest = orig })
	digests := map[string]string{
		"ghcr.io/platfarmai/svc-demo:1.0.0": "sha256:aaa111",
		"ghcr.io/platfarmai/svc-demo:2.0.0": "sha256:bbb222",
	}
	imageDigest = func(image string) (string, error) {
		if d, ok := digests[image]; ok {
			return d, nil
		}
		return "", os.ErrNotExist
	}

	// Given: empty releases → list says so
	var listBuf bytes.Buffer
	if err := releaseList(root, &listBuf); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(listBuf.String()); got != "no releases yet" {
		t.Fatalf("empty list: got %q", got)
	}

	// When: deploy v1 then v2
	if err := releaseDeploy(root, "svc-demo", "ghcr.io/platfarmai/svc-demo:1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := releaseDeploy(root, "svc-demo", "ghcr.io/platfarmai/svc-demo:2.0.0"); err != nil {
		t.Fatal(err)
	}

	f, err := loadReleases(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Services) != 2 {
		t.Fatalf("want 2 entries, got %d", len(f.Services))
	}
	if f.Services[0].Digest != "sha256:aaa111" || f.Services[0].Version != "1.0.0" {
		t.Fatalf("v1 entry: %+v", f.Services[0])
	}
	if f.Services[1].Digest != "sha256:bbb222" || f.Services[1].Version != "2.0.0" {
		t.Fatalf("v2 entry: %+v", f.Services[1])
	}

	// When: rollback → Then: prior digest printed + new rollback entry
	var rbBuf bytes.Buffer
	if err := releaseRollback(root, "svc-demo", &rbBuf); err != nil {
		t.Fatal(err)
	}
	out := rbBuf.String()
	if !strings.Contains(out, "ghcr.io/platfarmai/svc-demo@sha256:aaa111") {
		t.Fatalf("rollback output missing prior digest:\n%s", out)
	}
	if !strings.Contains(out, "docker compose up -d svc-demo") {
		t.Fatalf("rollback missing compose hint:\n%s", out)
	}

	f, err = loadReleases(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Services) != 3 {
		t.Fatalf("want 3 entries after rollback, got %d", len(f.Services))
	}
	last := f.Services[2]
	if last.Digest != "sha256:aaa111" || last.Note != "rollback" {
		t.Fatalf("rollback entry: %+v", last)
	}

	// Then: list renders all three
	listBuf.Reset()
	if err := releaseList(root, &listBuf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(listBuf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("list want 3 lines, got %d:\n%s", len(lines), listBuf.String())
	}
	if !strings.Contains(lines[0], "sha256:aaa111") || !strings.Contains(lines[1], "sha256:bbb222") {
		t.Fatalf("list lines:\n%s", listBuf.String())
	}
	if !strings.Contains(lines[2], "sha256:aaa111") {
		t.Fatalf("list rollback line:\n%s", lines[2])
	}
}
