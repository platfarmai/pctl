package main

import (
	"fmt"
	"regexp"
	"strings"
)

// 清单权限声明规范 v1 校验（specs/changes/003）。
// 纪律：清单只声明"存在什么"，判定规则永远在服务代码内。

var (
	declNameRE    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)  // 角色名（严格，不含点）
	scopeNameRE   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`) // scope 名（允许点号分层，如 data.orders.read）
	callRE        = regexp.MustCompile(`^(svc-[a-z0-9-]+):([a-z][a-z0-9_.-]{0,63})$`)
	tablePrefixRE = regexp.MustCompile(`^[a-z][a-z0-9_]*_$`)
)

// validatePermissionDecls 校验版本号、roles/exposes 声明，以及 calls↔exposes 跨清单引用。
func validatePermissionDecls(manifests []Manifest) []string {
	var problems []string

	// 汇总各服务暴露的 scope，供跨清单校验
	exposed := map[string]map[string]bool{} // svc-id -> scope set
	for _, m := range manifests {
		exposed[m.ID] = map[string]bool{}
		for _, s := range m.Exposes.Scopes {
			exposed[m.ID][s.Name] = true
		}
	}

	for _, m := range manifests {
		if m.SpecVersion != "" && m.SpecVersion != "v1" {
			problems = append(problems,
				fmt.Sprintf("%s: manifest 规范版本 %q 不支持（当前仅 v1）", m.ID, m.SpecVersion))
		}
		problems = append(problems, validateNamedDeclsRE(m.ID, "roles.vocabulary", m.Roles.Vocabulary, declNameRE)...)
		problems = append(problems, validateNamedDeclsRE(m.ID, "exposes.scopes", m.Exposes.Scopes, scopeNameRE)...)
		problems = append(problems, validateBootstrap(m)...)
		problems = append(problems, validateAdminUI(m)...)
		problems = append(problems, validateOpenAPI(m)...)
		if p := m.Data.TablePrefix; p != "" && !tablePrefixRE.MatchString(p) {
			problems = append(problems,
				fmt.Sprintf("%s: data.table_prefix %q 非法（须小写、以 _ 结尾，如 cms_）", m.ID, p))
		}

		for _, call := range m.Permissions.Calls {
			match := callRE.FindStringSubmatch(call)
			if match == nil {
				problems = append(problems,
					fmt.Sprintf("%s: permissions.calls %q 格式必须为 <svc-id>:<scope>", m.ID, call))
				continue
			}
			target, scope := match[1], match[2]
			scopes, ok := exposed[target]
			switch {
			case !ok:
				problems = append(problems,
					fmt.Sprintf("%s: calls 引用的服务 %s 不存在于平台（依赖需先安装）", m.ID, target))
			case !scopes[scope]:
				problems = append(problems,
					fmt.Sprintf("%s: 服务 %s 未在 exposes.scopes 声明 %q", m.ID, target, scope))
			}
		}
	}
	return problems
}

func validateNamedDeclsRE(id, field string, decls []NamedDecl, re *regexp.Regexp) []string {
	var problems []string
	seen := map[string]bool{}
	for _, d := range decls {
		if !re.MatchString(d.Name) {
			problems = append(problems,
				fmt.Sprintf("%s: %s 名称 %q 非法（须匹配 %s）", id, field, d.Name, re.String()))
		}
		if seen[d.Name] {
			problems = append(problems, fmt.Sprintf("%s: %s 名称 %q 重复", id, field, d.Name))
		}
		seen[d.Name] = true
	}
	return problems
}

func validateBootstrap(m Manifest) []string {
	if m.Roles.Bootstrap == "" {
		return nil
	}
	role, ok := strings.CutPrefix(m.Roles.Bootstrap, "platform-admin=")
	if !ok {
		return []string{fmt.Sprintf("%s: roles.bootstrap 仅允许 platform-admin=<role> 形式", m.ID)}
	}
	for _, d := range m.Roles.Vocabulary {
		if d.Name == role {
			return nil
		}
	}
	return []string{fmt.Sprintf("%s: roles.bootstrap 引用的角色 %q 未在 vocabulary 声明", m.ID, role)}
}

// validateAdminUI 校验 admin_ui（specs/006）：声明了 path 才检查，路径必须相对且非空。
func validateAdminUI(m Manifest) []string {
	if m.AdminUI.Path == "" {
		return nil
	}
	if !strings.HasPrefix(m.AdminUI.Path, "/") {
		return []string{fmt.Sprintf("%s: admin_ui.path %q 必须以 / 开头（相对 mount.path）", m.ID, m.AdminUI.Path)}
	}
	if strings.Contains(m.AdminUI.Path, "..") {
		return []string{fmt.Sprintf("%s: admin_ui.path %q 不得包含 ..", m.ID, m.AdminUI.Path)}
	}
	return nil
}

// validateOpenAPI 校验 open_api（specs/009）：route 格式合法、path 在 mount.path 下、
// scope 必须在本服务 exposes.scopes 声明。
func validateOpenAPI(m Manifest) []string {
	if len(m.OpenAPI) == 0 {
		return nil
	}
	declared := map[string]bool{}
	for _, s := range m.Exposes.Scopes {
		declared[s.Name] = true
	}
	var problems []string
	for _, o := range m.OpenAPI {
		fields := strings.Fields(o.Route)
		if len(fields) != 2 {
			problems = append(problems,
				fmt.Sprintf("%s: open_api.route %q 格式必须为 \"METHOD /path\"", m.ID, o.Route))
			continue
		}
		if !strings.HasPrefix(o.Path(), m.Mount.Path) {
			problems = append(problems,
				fmt.Sprintf("%s: open_api.route %q 的路径须在 mount.path %q 下", m.ID, o.Route, m.Mount.Path))
		}
		if o.Scope == "" {
			problems = append(problems, fmt.Sprintf("%s: open_api.route %q 缺 scope", m.ID, o.Route))
		} else if !declared[o.Scope] {
			problems = append(problems,
				fmt.Sprintf("%s: open_api scope %q 未在 exposes.scopes 声明", m.ID, o.Scope))
		}
	}
	return problems
}
