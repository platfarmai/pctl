package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func catalogManifests() []Manifest {
	pay := openManifest(
		OpenRoute{Route: "POST /api/pay/v1/orders", Scope: "pay.order.create"},
		OpenRoute{Route: "GET /api/pay/v1/orders/*", Scope: "pay.order.query"},
		OpenRoute{Route: "POST /api/pay/v1/orders/*/close", Scope: "pay.order.create"},
	)
	pay.Exposes.Scopes = []NamedDecl{
		{Name: "pay.order.create", Desc: "创建订单 it's $5"},
		{Name: "pay.order.query", Desc: "查询订单"},
		{Name: "pay.internal", Desc: "只给服务 token"},
	}
	console := apiManifest("svc-openapi")
	console.Runtime.Env = []string{"OPENAPI_CLIENT_ID", "PF_SCOPE_CATALOG"}
	return []Manifest{console, pay}
}

func TestScopeCatalog_whenScopeHasNoOpenRoute_thenOmitted(t *testing.T) {
	got := scopeCatalog(catalogManifests())
	want := []scopeCatalogEntry{
		{Service: "svc-pay", Name: "pay.order.create", Desc: "创建订单 it's $5",
			Routes: []string{"POST /api/pay/v1/orders", "POST /api/pay/v1/orders/*/close"}},
		{Service: "svc-pay", Name: "pay.order.query", Desc: "查询订单", Routes: []string{"GET /api/pay/v1/orders/*"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// 声明了 PF_SCOPE_CATALOG 的服务拿到的是清单生成的目录本身，而不是 ${PF_SCOPE_CATALOG} 占位：
// 引号和 $ 必须转义到 compose 读出来还是原 JSON。
func TestRenderServicesCompose_whenServiceWantsScopeCatalog_thenInlineJSON(t *testing.T) {
	manifests := catalogManifests()
	out := renderServicesCompose(manifests, true)

	var doc struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("generated compose is not valid YAML: %v\n%s", err, out)
	}
	raw, ok := doc.Services["svc-openapi"].Environment["PF_SCOPE_CATALOG"]
	if !ok {
		t.Fatalf("PF_SCOPE_CATALOG missing:\n%s", out)
	}
	// compose 把 $$ 还原成 $。
	var got []scopeCatalogEntry
	if err := json.Unmarshal([]byte(unescapeComposeDollar(raw)), &got); err != nil {
		t.Fatalf("catalog is not JSON: %v\n%s", err, raw)
	}
	if !reflect.DeepEqual(got, scopeCatalog(manifests)) {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if _, leaked := doc.Services["svc-pay"].Environment["PF_SCOPE_CATALOG"]; leaked {
		t.Fatal("services that did not declare PF_SCOPE_CATALOG must not receive it")
	}
}

func unescapeComposeDollar(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '$' {
			i++
		}
		out = append(out, s[i])
	}
	return string(out)
}
