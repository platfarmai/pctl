package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopedEnvName_whenHyphenatedID_thenUpperSnake(t *testing.T) {
	if got := scopedEnvName("svc-ads", "DATABASE_URL"); got != "SVC_ADS_DATABASE_URL" {
		t.Fatalf("got %q", got)
	}
	if got := scopedEnvName("auth", "DATABASE_URL"); got != "AUTH_DATABASE_URL" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderHealthcheck_whenDefault_thenNoWgetDependency(t *testing.T) {
	// 精简镜像没有 wget，默认探活不能依赖它，否则服务正常也会一直 unhealthy。
	var b strings.Builder
	renderHealthcheck(&b, Manifest{}, 8080)
	out := b.String()
	if strings.Contains(out, "wget") {
		t.Fatalf("default healthcheck must not need wget:\n%s", out)
	}
	if !strings.Contains(out, "kill -0 1") {
		t.Fatalf("want PID check, got:\n%s", out)
	}
}

func TestRenderHealthcheck_whenHTTPOptIn_thenUsesReadyz(t *testing.T) {
	var m Manifest
	m.Runtime.Healthcheck = "http"
	var b strings.Builder
	renderHealthcheck(&b, m, 9000)
	out := b.String()
	if !strings.Contains(out, "http://127.0.0.1:9000/readyz") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestRenderHealthcheck_whenNone_thenDisabled(t *testing.T) {
	var m Manifest
	m.Runtime.Healthcheck = "none"
	var b strings.Builder
	renderHealthcheck(&b, m, 8080)
	out := b.String()
	if !strings.Contains(out, "disable: true") {
		t.Fatalf("got:\n%s", out)
	}
	if strings.Contains(out, "interval") {
		t.Fatalf("disabled healthcheck must not emit interval:\n%s", out)
	}
}

func TestServicesDirs_whenUnset_thenDefaultServices(t *testing.T) {
	root := t.TempDir()
	got := servicesDirs(root)
	want := filepath.Join(root, "services")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v want [%s]", got, want)
	}
}

func TestServicesDirs_whenEnvSet_thenResolvedRelativeToRoot(t *testing.T) {
	// 镜像部署没有 services/，插件源码放在 _src/services，必须能指过去。
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"),
		[]byte("PF_SERVICES_DIR=_src/services\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := servicesDirs(root)
	want := filepath.Join(root, "_src", "services")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v want [%s]", got, want)
	}
}

func TestLoadManifests_whenCustomServicesDir_thenFound(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "_src", "services", "svc-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "id: svc-demo\nname: Demo\ntrust: first-party\nenabled: true\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"),
		[]byte("PF_SERVICES_DIR=_src/services\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadManifests(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "svc-demo" {
		t.Fatalf("got %+v", got)
	}
}
