package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func openManifest(routes ...OpenRoute) Manifest {
	m := apiManifest("svc-pay")
	m.Exposes.Scopes = []NamedDecl{{Name: "pay.order.create"}, {Name: "pay.order.query"}}
	m.OpenAPI = routes
	return m
}

func TestOpenRouteKongPath(t *testing.T) {
	cases := []struct{ route, want string }{
		{"POST /api/pay/v1/orders", "/api/pay/v1/orders"},
		{"GET /api/pay/v1/orders/*", "~/api/pay/v1/orders/[^/]+$"},
		{"POST /api/pay/v1/orders/*/close", "~/api/pay/v1/orders/[^/]+/close$"},
		{"GET /api/pay/v1.1/orders/*", `~/api/pay/v1\.1/orders/[^/]+$`},
		{"GET /api/pay/v1/*/items/*", "~/api/pay/v1/[^/]+/items/[^/]+$"},
	}
	for _, c := range cases {
		if got := (OpenRoute{Route: c.route}).KongPath(); got != c.want {
			t.Errorf("%s: got %q, want %q", c.route, got, c.want)
		}
	}
}

// 生成的 kong.yml 里不能再出现字面 `*`：Kong 不把它当通配符，这样的路由永远匹配不到请求。
func TestRenderKong_whenOpenRouteHasWildcard_thenRegexPath(t *testing.T) {
	m := openManifest(
		OpenRoute{Route: "POST /api/pay/v1/orders", Scope: "pay.order.create"},
		OpenRoute{Route: "GET /api/pay/v1/orders/*", Scope: "pay.order.query"},
		OpenRoute{Route: "POST /api/pay/v1/orders/*/close", Scope: "pay.order.create"},
		OpenRoute{Route: "GET /api/pay/v1.1/orders/*", Scope: "pay.order.query"},
	)
	out := renderKong([]Manifest{m}, "", false)

	var doc struct {
		Services []struct {
			Name   string `yaml:"name"`
			Routes []struct {
				Name    string   `yaml:"name"`
				Paths   []string `yaml:"paths"`
				Methods []string `yaml:"methods"`
			} `yaml:"routes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("generated kong.yml is not valid YAML: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, s := range doc.Services {
		for _, r := range s.Routes {
			if len(r.Paths) != 1 {
				t.Fatalf("route %s: paths = %v", r.Name, r.Paths)
			}
			if strings.Contains(r.Paths[0], "*") {
				t.Errorf("route %s still carries a literal wildcard: %q", r.Name, r.Paths[0])
			}
			got[r.Name] = strings.Join(r.Methods, ",") + " " + r.Paths[0]
		}
	}
	want := map[string]string{
		"svc-pay-open-0": "POST /api/pay/v1/orders",
		"svc-pay-open-1": "GET ~/api/pay/v1/orders/[^/]+$",
		"svc-pay-open-2": "POST ~/api/pay/v1/orders/[^/]+/close$",
		"svc-pay-open-3": `GET ~/api/pay/v1\.1/orders/[^/]+$`,
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: got %q, want %q", name, got[name], w)
		}
	}
	// 不含通配符的路径保持双引号写法，存量服务的生成结果不变。
	if !strings.Contains(out, `paths: ["/api/pay/v1/orders"]`) {
		t.Errorf("plain open route must keep its double-quoted prefix path:\n%s", out)
	}
}

func TestValidateManifests_whenOpenRouteWildcardNotWholeSegment_thenError(t *testing.T) {
	for _, route := range []string{"GET /api/pay/v1/orders/a*", "GET /api/pay/v1/orders/**"} {
		m := openManifest(OpenRoute{Route: route, Scope: "pay.order.query"})
		if err := validateManifests([]Manifest{m}); err == nil || !strings.Contains(err.Error(), "必须独占一段") {
			t.Errorf("%s: got %v", route, err)
		}
	}
}

func TestValidateManifests_whenOpenRouteWildcardWholeSegment_thenNoError(t *testing.T) {
	m := openManifest(
		OpenRoute{Route: "GET /api/pay/v1/orders/*", Scope: "pay.order.query"},
		OpenRoute{Route: "POST /api/pay/v1/orders/*/close", Scope: "pay.order.create"},
	)
	if err := validateManifests([]Manifest{m}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
