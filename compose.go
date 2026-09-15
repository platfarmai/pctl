package main

import (
	"fmt"
	"strings"
)

// renderServicesCompose 生成 docker-compose.services.yml：
// gateway（多宿主进所有插件网络）+ plugin-pg（有第三方插件时）+ 各服务 + 插件网络。
// gateway/plugin-pg 放生成文件的原因：它们的 networks 列表随插件增减（附录 H.4）。
func renderServicesCompose(manifests []Manifest) string {
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
	renderGateway(&b, pluginNets)
	if hasThirdParty {
		renderPluginPG(&b, pluginNets)
	}
	for _, m := range manifests {
		renderService(&b, m)
	}
	if len(pluginNets) > 0 {
		b.WriteString("\nnetworks:\n")
		for _, n := range pluginNets {
			fmt.Fprintf(&b, "  %s:\n    internal: true # 断外网；需 egress 的插件走代理（附录 H.4）\n", n)
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
		fmt.Fprintf(b, "    build: ./services/%s\n", m.ID)
	}
	if len(m.Runtime.Env) > 0 || len(m.Auth.AcceptServiceTokens) > 0 || m.Data.TablePrefix != "" {
		b.WriteString("    environment:\n")
		for _, v := range m.Runtime.Env {
			if v == "PF_REDIS_URL" {
				fmt.Fprintf(b, "      %s: ${PF_REDIS_URL:-redis://redis:6379/0}\n", v)
				continue
			}
			fmt.Fprintf(b, "      %s: ${%s}\n", v, v)
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
	fmt.Fprintf(b, "    healthcheck:\n      test: [\"CMD\", \"wget\", \"-qO-\", \"http://127.0.0.1:%d/readyz\"]\n      interval: 5s\n      timeout: 3s\n      retries: 3\n      start_period: 15s\n", port)
	b.WriteString("    networks:\n" + netList([]string{m.NetworkName()}))
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
