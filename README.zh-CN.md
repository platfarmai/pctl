# pctl — PlatFarm 平台 CLI

[English](README.md) | [简体中文](README.zh-CN.md)

[PlatFarm](https://github.com/platfarmai/platfarm) 的命令行工具：清单、Kong/compose 生成、插件安装、密钥初始化、管理台二进制。

本仓库**只发 CLI**。平台镜像（`auth` / `console` / `runtime-*`）仍在 [platfarmai/platfarm](https://github.com/platfarmai/platfarm)，打 `v*` 推 GHCR。

## 安装

从 [Releases](https://github.com/platfarmai/pctl/releases) 下载（**不要**把二进制提交进 git）：

```bash
VER=v0.0.1
curl -fsSL -o pctl "https://github.com/platfarmai/pctl/releases/download/${VER}/pctl_${VER}_linux_amd64"
chmod +x pctl
./pctl init .    # 空目录生成 .keys/ + gateway/kong.yml
```

或：`go build -o pctl .`

## 命令

| 命令 | 是否需要完整 PlatFarm 树 | 作用 |
|---|---|---|
| `pctl init [dir]` | 否 | 拉镜像启动用的密钥与最小 kong.yml |
| `pctl new / sync / check / list` | 是 | 脚手架与网关生成 |
| `pctl install / …` | 是 | 插件生命周期 |
| `pctl serve` | 是 | 管理台 |

一键 Compose：[platfarm 说明](https://github.com/platfarmai/platfarm/blob/main/docs/docker-compose-quickstart.zh-CN.md)。

## 发版

本仓打 `v*` → Actions 交叉编译并挂到 Release。**不**在本仓打容器镜像。
