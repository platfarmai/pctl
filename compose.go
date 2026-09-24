package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// renderServicesCompose 生成 docker-compose.services.yml：
// gateway（多宿主进所有插件网络）+ plugin-pg（有第三方插件时）+ 各服务 + 插件网络。
// gateway/plugin-pg 放生成文件的原因：它们的 networks 列表随插件增减（附录 H.4）。
//
// servicesOnly=true 时只输出服务定义，不含 gateway / networks / volumes：
// 镜像部署（compose.release.yml）的运行目录自带 gateway 与网络，两边都定义会冲突
// （"service gateway depends on undefined service redis"）。开关见 PF_COMPOSE_SERVICES_ONLY。
func renderServicesCompose(manifests []Manifest, servicesOnly bool) string {
	var pluginNets []string
	hasThirdParty := false
	for _, m := range manifests {
		if m.IsThirdParty() {
			hasThirdParty = true
			pluginNets = append(pluginNets, m.NetworkName())
		}
	}

	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("services:\n")
	if !servicesOnly {
		renderGateway(&b, pluginNets)
		if hasThirdParty {
			renderPluginPG(&b, pluginNets)
		}
	}
	hasEgress := false
	for _, m := range manifests {
		renderService(&b, m)
		if m.IsThirdParty() && len(m.Permissions.Egress) > 0 {
			renderEgressProxy(&b, m)
			hasEgress = true
		}
	}
	if servicesOnly {
		// 网络与卷由运行目录的 compose.yml 负责，这里重复定义会冲突。
		return b.String()
	}
	if len(pluginNets) > 0 || hasEgress {
		b.WriteString("\nnetworks:\n")
		for _, n := range pluginNets {
			fmt.Fprintf(&b, "  %s:\n    internal: true # 断外网；需 egress 的插件走代理（附录 H.4 + specs/024）\n", n)
		}
		if hasEgress {
			b.WriteString("  egress-net: {} # egress sidecar 的出网侧（specs/024）\n")
		}
	}
	if hasThirdParty {
		b.WriteString("\nvolumes:\n  pf-plugin-pg-data:\n")
	}
	return b.String()
}

func renderGateway(b *strings.Builder, pluginNets []string) {
	b.WriteString(`  gateway:
    image: kong:3.9
    environment:
      KONG_DATABASE: "off"
      KONG_DECLARATIVE_CONFIG: /kong/kong.yml
      KONG_PROXY_ACCESS_LOG: /dev/stdout
      KONG_PROXY_ERROR_LOG: /dev/stderr
      KONG_ADMIN_LISTEN: "127.0.0.1:8001"
      KONG_NGINX_WORKER_PROCESSES: ${KONG_NGINX_WORKER_PROCESSES:-1} # Redis 限流时可改为 auto
      KONG_UNTRUSTED_LUA: sandbox # pre-function 需 require（SSO/scope 校验 specs/006+009，响应加密附录 F）
      KONG_UNTRUSTED_LUA_SANDBOX_REQUIRES: cjson,resty.openssl.kdf,resty.openssl.cipher,resty.random
    ports:
      - "18000:8000"
    volumes:
      - ./gateway/kong.yml:/kong/kong.yml:ro
    depends_on:
      auth:
        condition: service_healthy` + redisDepends() + `
    healthcheck:
      test: ["CMD", "kong", "health"]
      interval: 5s
      timeout: 3s
      retries: 10
    networks:
` + netList(append([]string{"core-net"}, pluginNets...)))
}

func renderPluginPG(b *strings.Builder, pluginNets []string) {
	b.WriteString(`  plugin-pg: # 插件专属 PG 实例：平台 PG 与插件网络物理隔离（附录 H.3）
    image: postgres:18-alpine
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: ${PLUGIN_PG_PASSWORD:-plugin_pg_local}
    volumes:
      - pf-plugin-pg-data:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 3s
      retries: 10
    networks:
` + netList(append([]string{"core-net"}, pluginNets...)))
}

func renderService(b *strings.Builder, m Manifest) {
	fmt.Fprintf(b, "  %s:\n", m.ID)
	if m.IsThirdParty() {
		image := m.Source.Image
		if m.Source.Digest != "" {
			image = strings.SplitN(image, "@", 2)[0] + "@" + m.Source.Digest
		}
		fmt.Fprintf(b, "    image: %s\n", image)
		fmt.Fprintf(b, "    env_file: [.env.plugins/%s.env]\n", m.ID)
		b.WriteString("    read_only: true\n    security_opt: [\"no-new-privileges:true\"]\n")
		if m.Resources.Memory != "" {
			fmt.Fprintf(b, "    mem_limit: %s\n", m.Resources.Memory)
		}
		if m.Resources.Cpus != "" {
			fmt.Fprintf(b, "    cpus: %s\n", m.Resources.Cpus)
		}
	} else {
		fmt.Fprintf(b, "    build: %s\n", buildContext(m))
	}
	hasEgress := m.IsThirdParty() && len(m.Permissions.Egress) > 0
	if len(m.Runtime.Env) > 0 || len(m.Auth.AcceptServiceTokens) > 0 || m.Data.TablePrefix != "" || hasEgress {
		b.WriteString("    environment:\n")
		if hasEgress { // specs/024：出网只能走白名单代理
			fmt.Fprintf(b, "      HTTP_PROXY: http://egress-%s:3128\n", m.ID)
			fmt.Fprintf(b, "      HTTPS_PROXY: http://egress-%s:3128\n", m.ID)
			fmt.Fprintf(b, "      http_proxy: http://egress-%s:3128\n", m.ID)
			fmt.Fprintf(b, "      https_proxy: http://egress-%s:3128\n", m.ID)
			b.WriteString("      NO_PROXY: gateway,plugin-pg,localhost,127.0.0.1\n")
			b.WriteString("      no_proxy: gateway,plugin-pg,localhost,127.0.0.1\n")
		}
		for _, v := range m.Runtime.Env {
			if v == "PF_REDIS_URL" {
				fmt.Fprintf(b, "      %s: ${PF_REDIS_URL:-redis://redis:6379/0}\n", v)
				continue
			}
			if v == "LOKI_URL" { // specs/013：默认空 → 用量视图显示"未启用"
				fmt.Fprintf(b, "      %s: ${LOKI_URL:-}\n", v)
				continue
			}
			// 服务专属变量优先，回落到同名全局变量。
			// 否则每个服务的 DATABASE_URL 都指向 ${DATABASE_URL}，
			// 改成插件库就会让 auth 连错库（auth 与插件本就该用不同数据库）。
			fmt.Fprintf(b, "      %s: ${%s:-${%s}}\n", v, scopedEnvName(m.ID, v), v)
		}
		if len(m.Auth.AcceptServiceTokens) > 0 {
			fmt.Fprintf(b, "      PF_ACCEPT_SERVICE_TOKENS: %s\n", strings.Join(m.Auth.AcceptServiceTokens, ","))
		}
		if m.Data.TablePrefix != "" { // specs/007：仅非空时注入
			fmt.Fprintf(b, "      PF_TABLE_PREFIX: %s\n", m.Data.TablePrefix)
		}
	}
	// 验签公钥挂载：第一方默认给；第三方按 needs_identity（公钥非密，可安全下发）
	if !m.IsThirdParty() || m.Permissions.NeedsIdentity {
		b.WriteString("    volumes:\n      - ./.keys/pf-auth.pem.pub:/pf/jwt.pub:ro\n")
	}
	drain := m.Runtime.DrainSeconds
	if drain <= 0 {
		drain = 25
	}
	port := m.Runtime.Port
	if port <= 0 {
		port = 8080
	}
	fmt.Fprintf(b, "    stop_grace_period: %ds\n", drain+5)
	renderHealthcheck(b, m, port)
	b.WriteString("    networks:\n" + netList([]string{m.NetworkName()}))
}

// composeServicesOnly 判断是否只输出服务定义。
// 显式设 PF_COMPOSE_SERVICES_ONLY 优先；否则自动检测：运行目录的 compose.yml
// 若已定义 gateway（镜像部署 compose.release.yml 就是如此），生成文件里再定义一次会冲突。
func composeServicesOnly(root string) bool {
	switch strings.ToLower(strings.TrimSpace(envFromRoot(root, "PF_COMPOSE_SERVICES_ONLY"))) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	}
	raw, err := os.ReadFile(filepath.Join(root, "compose.yml"))
	if err != nil {
		return false
	}
	return regexp.MustCompile(`(?m)^\s{2}gateway:`).Match(raw)
}

// buildContext 返回第一方服务的构建上下文（相对运行目录）。
// 清单可能来自 PF_SERVICES_DIR 指定的其它目录（如 _src/services），
// 写死 ./services/<id> 会让 compose 找不到 Dockerfile。
func buildContext(m Manifest) string {
	if m.Dir == "" {
		return "./services/" + m.ID
	}
	rel, err := filepath.Rel(m.Root, m.Dir)
	if err != nil {
		return "./services/" + m.ID
	}
	return "./" + filepath.ToSlash(rel)
}

// scopedEnvName 把 svc-ads + DATABASE_URL 变成 SVC_ADS_DATABASE_URL，
// 让每个服务能独立配置同名变量，互不影响。
func scopedEnvName(id, name string) string {
	prefix := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(id))
	return prefix + "_" + name
}

// renderHealthcheck 生成健康检查。
// 默认探活方式不能假设镜像里有 wget：精简 Alpine / distroless / scratch 都没有，
// 会导致服务明明正常却一直 unhealthy。默认改用 PID 1 存活检查（任何镜像都可用）。
// 需要真正的就绪探测时，在清单里写 runtime.healthcheck: http（要求镜像自带 wget 或 curl）。
func renderHealthcheck(b *strings.Builder, m Manifest, port int) {
	b.WriteString("    healthcheck:\n")
	switch strings.ToLower(strings.TrimSpace(m.Runtime.Healthcheck)) {
	case "http":
		fmt.Fprintf(b, "      test: [\"CMD\", \"wget\", \"-qO-\", \"http://127.0.0.1:%d/readyz\"]\n", port)
	case "none":
		b.WriteString("      disable: true\n")
		return
	default:
		b.WriteString("      test: [\"CMD-SHELL\", \"kill -0 1\"]\n")
	}
	b.WriteString("      interval: 5s\n      timeout: 3s\n      retries: 3\n      start_period: 15s\n")
}

// renderEgressProxy 域名白名单出网代理 sidecar（specs/024）：
// 双宿主（插件网 + egress-net），插件容器经 HTTP(S)_PROXY 出网，白名单外一律拒绝。
func renderEgressProxy(b *strings.Builder, m Manifest) {
	fmt.Fprintf(b, "  egress-%s:\n", m.ID)
	b.WriteString("    image: ubuntu/squid:latest\n")
	fmt.Fprintf(b, "    volumes:\n      - ./gateway/egress-%s.conf:/etc/squid/squid.conf:ro\n", m.ID)
	b.WriteString("    networks:\n" + netList([]string{m.NetworkName(), "egress-net"}))
}

func redisDepends() string {
	if redisRateLimit() {
		return `
      redis:
        condition: service_started`
	}
	return ""
}

func netList(nets []string) string {
	var b strings.Builder
	for _, n := range nets {
		b.WriteString("      - " + n + "\n")
	}
	return b.String()
}
