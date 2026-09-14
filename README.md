# pctl — PlatFarm platform CLI

[English](README.md) | [简体中文](README.zh-CN.md)

Command-line tool for [PlatFarm](https://github.com/platfarmai/platfarm): manifests, Kong/compose generation, plugin install, keys bootstrap, and the admin console binary.

This repository is **CLI-only**. Platform images (`auth`, `console`) stay in [platfarmai/platfarm](https://github.com/platfarmai/platfarm) and publish to GHCR on `v*` tags.

## Install

Download from [Releases](https://github.com/platfarmai/pctl/releases) (do not commit binaries into git):

| Asset | Platform |
|---|---|
| `pctl_<tag>_linux_amd64` | Linux x86_64 |
| `pctl_<tag>_linux_arm64` | Linux arm64 |
| `pctl_<tag>_darwin_amd64` / `darwin_arm64` | macOS |
| `pctl_<tag>_windows_amd64.exe` | Windows |

```bash
VER=v0.0.1
curl -fsSL -o pctl "https://github.com/platfarmai/pctl/releases/download/${VER}/pctl_${VER}_linux_amd64"
chmod +x pctl
./pctl init .    # empty dir → .keys/ + gateway/kong.yml (no Go, no full checkout)
```

Or build from source:

```bash
go build -o pctl .
```

## Commands

| Command | Needs full PlatFarm tree? | Purpose |
|---|---|---|
| `pctl init [dir]` | No | Keys + minimal `gateway/kong.yml` for GHCR compose |
| `pctl new / sync / check / list` | Yes | Scaffolds and gateway generation |
| `pctl install / enable / …` | Yes | Plugin lifecycle |
| `pctl serve` | Yes (or console image) | Admin UI |

Full stack quickstart: [platfarm docker-compose guide](https://github.com/platfarmai/platfarm/blob/main/docs/docker-compose-quickstart.md).

## Release

Push a tag `v*` on this repo → GitHub Actions builds multi-platform binaries and attaches them to the Release. Platform container images are **not** built here.
