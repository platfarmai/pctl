package main

import (
	"fmt"
	"regexp"
	"strings"
)

// 清单权限声明规范 v1 校验（specs/changes/003）。
// 纪律：清单只声明"存在什么"，判定规则永远在服务代码内。

var (
	declNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	callRE     = regexp.MustCompile(`^(svc-[a-z0-9-]+):([a-z][a-z0-9_-]{0,31})$`)
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
		problems = append(problems, validateNamedDecls(m.ID, "roles.vocabulary", m.Roles.Vocabulary)...)
		problems = append(problems, validateNamedDecls(m.ID, "exposes.scopes", m.Exposes.Scopes)...)
		problems = append(problems, validateBootstrap(m)...)

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

func validateNamedDecls(id, field string, decls []NamedDecl) []string {
	var problems []string
	seen := map[string]bool{}
	for _, d := range decls {
		if !declNameRE.MatchString(d.Name) {
			problems = append(problems,
				fmt.Sprintf("%s: %s 名称 %q 非法（^[a-z][a-z0-9_-]{0,31}$）", id, field, d.Name))
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
