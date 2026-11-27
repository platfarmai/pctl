package main

import (
	"os"
	"strings"
	"testing"
)

func websiteManifest(id string) Manifest {
	var m Manifest
	m.ID = id
	m.Dir = "services/" + id
	m.Mount.Path = "/"
	m.Mount.Website = true
	m.Runtime.Port = 8080
	return m
}

func apiManifest(id string) Manifest {
	var m Manifest
	m.ID = id
	m.Dir = "services/" + id
	m.Mount.Path = "/api/" + strings.TrimPrefix(id, "svc-")
	m.Runtime.Port = 8080
	return m
}

func TestValidateManifests_whenWebsiteValid_thenNoError(t *testing.T) {
	manifests := []Manifest{apiManifest("svc-demo"), websiteManifest("svc-site")}
	if err := validateManifests(manifests); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateManifests_whenWebsitePathNotRoot_thenError(t *testing.T) {
	m := websiteManifest("svc-site")
	m.Mount.Path = "/site"
	if err := validateManifests([]Manifest{m}); err == nil || !strings.Contains(err.Error(), "mount.path 必须为") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateManifests_whenTwoWebsites_thenError(t *testing.T) {
	manifests := []Manifest{websiteManifest("svc-site-a"), websiteManifest("svc-site-b")}
	if err := validateManifests(manifests); err == nil || !strings.Contains(err.Error(), "根站点插件重复") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateManifests_whenWebsiteRequiresAuth_thenError(t *testing.T) {
	m := websiteManifest("svc-site")
	m.Auth.Required = true
	if err := validateManifests([]Manifest{m}); err == nil || !strings.Contains(err.Error(), "auth.required=false") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateManifests_whenWebsiteThirdParty_thenError(t *testing.T) {
	m := websiteManifest("svc-site")
	m.Trust = "third-party"
	m.Source.Image = "example.com/site:latest"
	if err := validateManifests([]Manifest{m}); err == nil || !strings.Contains(err.Error(), "必须为 first-party") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateManifests_whenWebsiteDeclaresPublicRoutes_thenError(t *testing.T) {
	m := websiteManifest("svc-site")
	m.Mount.PublicRoutes = []string{"GET /ping"}
	if err := validateManifests([]Manifest{m}); err == nil || !strings.Contains(err.Error(), "不支持 public_routes") {
		t.Fatalf("got %v", err)
	}
}

func TestRenderKong_whenNoWebsite_thenPlatformRootUnchanged(t *testing.T) {
	out := renderKong([]Manifest{apiManifest("svc-demo")}, "", false)
	if !strings.Contains(out, `name: platform-root`) {
		t.Fatalf("missing platform-root:\n%s", out)
	}
	if strings.Contains(out, "platform-info") {
		t.Fatalf("platform-info should not appear without a website plugin:\n%s", out)
	}
}

func TestRenderKong_whenWebsite_thenRootRouteMovesToSiteAndIndexMovesToInfo(t *testing.T) {
	site := websiteManifest("svc-site")
	site.Limits.RatePerMinute = 60
	out := renderKong([]Manifest{apiManifest("svc-demo"), site}, "", false)

	if strings.Contains(out, "name: platform-root") {
		t.Fatalf("platform-root must be replaced when a website plugin is present:\n%s", out)
	}
	if !strings.Contains(out, `name: platform-info`) || !strings.Contains(out, `paths: ["/platform/info"]`) {
		t.Fatalf("platform index must move to /platform/info:\n%s", out)
	}
	if !strings.Contains(out, `name: oauth-token`) || !strings.Contains(out, `paths: ["/oauth/token"]`) {
		t.Fatalf("/oauth/token must remain routed to auth when a website plugin owns /:\n%s", out)
	}
	if !strings.Contains(out, "name: svc-site-root") || !strings.Contains(out, `paths: ["/"]`) {
		t.Fatalf("website plugin must own the root route:\n%s", out)
	}
	// 根站点路由不应再叠加 -protected / -public 等常规路由。
	if strings.Contains(out, "svc-site-protected") || strings.Contains(out, "svc-site-public") {
		t.Fatalf("website plugin must not emit regular api routes:\n%s", out)
	}
	// 普通服务路由不受影响。
	if !strings.Contains(out, "name: svc-demo-protected") {
		t.Fatalf("regular services must still get a -protected route:\n%s", out)
	}
}

func TestValidateManifests_whenBrowserPagesUnknown_thenError(t *testing.T) {
	m := websiteManifest("svc-site")
	m.Mount.BrowserPages = []string{"/docs", "/auth/login"}
	if err := validateManifests([]Manifest{m}); err == nil || !strings.Contains(err.Error(), `browser_pages 不支持 "/auth/login"`) {
		t.Fatalf("got %v", err)
	}
}

func TestValidateManifests_whenBrowserPagesDuplicated_thenError(t *testing.T) {
	m := websiteManifest("svc-site")
	m.Mount.BrowserPages = []string{"/docs", "/docs"}
	if err := validateManifests([]Manifest{m}); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateManifests_whenBrowserPagesOnApiService_thenError(t *testing.T) {
	m := apiManifest("svc-demo")
	m.Mount.BrowserPages = []string{"/docs"}
	if err := validateManifests([]Manifest{m}); err == nil || !strings.Contains(err.Error(), "只能用于根站点插件") {
		t.Fatalf("got %v", err)
	}
}

func TestRenderKong_whenWebsiteWithoutBrowserPages_thenNoHeaderRoutes(t *testing.T) {
	out := renderKong([]Manifest{websiteManifest("svc-site")}, "", false)
	if strings.Contains(out, "svc-site-page-") || strings.Contains(out, "headers:") {
		t.Fatalf("browser page routes must be opt-in:\n%s", out)
	}
}

func TestRenderKong_whenBrowserPages_thenHtmlRequestsGoToSiteAndJsonStaysOnAuth(t *testing.T) {
	site := websiteManifest("svc-site")
	site.Mount.BrowserPages = []string{"/docs", "/platform/info"}
	if err := validateManifests([]Manifest{site}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := renderKong([]Manifest{site}, "", false)

	for _, want := range []string{
		"      - name: svc-site-page-0\n        paths: [\"/docs\"]\n        headers: { accept: ['~*text/html'] }\n        strip_path: false\n",
		"      - name: svc-site-page-1\n        paths: [\"/platform/info\"]\n        headers: { accept: ['~*text/html'] }\n        strip_path: false\n",
		// JSON 路由原样保留在 auth 上，程序调用不受影响。
		"      - name: platform-info\n        paths: [\"/platform/info\"]\n        strip_path: true\n",
		"      - name: platform-docs\n        paths: [\"/docs\"]\n        strip_path: false\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing route block %q in:\n%s", want, out)
		}
	}
}

func TestRedisDepends_whenExternal_thenNoDependsBlock(t *testing.T) {
	t.Setenv("PF_REDIS_URL", "redis://redis:6379/0")
	t.Setenv("PF_REDIS_EXTERNAL", "1")
	if got := redisDepends(); got != "" {
		t.Fatalf("expected no depends_on block for external redis, got %q", got)
	}
}

func TestRedisDepends_whenNotExternal_thenDependsBlockPresent(t *testing.T) {
	t.Setenv("PF_REDIS_URL", "redis://redis:6379/0")
	os.Unsetenv("PF_REDIS_EXTERNAL")
	if got := redisDepends(); !strings.Contains(got, "condition: service_started") {
		t.Fatalf("expected depends_on redis block, got %q", got)
	}
}
