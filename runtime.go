package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// containerCLI 返回 compose 所用可执行文件：PF_CONTAINER_CLI 或 PATH 上第一个 docker|podman|nerdctl。
func containerCLI() string {
	if v := strings.TrimSpace(os.Getenv("PF_CONTAINER_CLI")); v != "" {
		return v
	}
	for _, name := range []string{"docker", "podman", "nerdctl"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return "docker"
}

// engineSocket 探测容器引擎 API 套接字（console 用）。
// 顺序：PF_CONTAINER_HOST → DOCKER_HOST → docker.sock → podman.sock。
func engineSocket() (network, addr string) {
	if v := strings.TrimSpace(os.Getenv("PF_CONTAINER_HOST")); v != "" {
		return parseEngineHost(v)
	}
	if v := strings.TrimSpace(os.Getenv("DOCKER_HOST")); v != "" {
		return parseEngineHost(v)
	}
	if runtime.GOOS == "windows" {
		return "npipe", `\\.\pipe\docker_engine`
	}
	candidates := []string{
		"/var/run/docker.sock",
		"/run/docker.sock",
		fmt.Sprintf("/run/user/%d/podman/podman.sock", os.Getuid()),
		"/run/podman/podman.sock",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return "unix", p
		}
	}
	return "unix", "/var/run/docker.sock"
}

func parseEngineHost(host string) (network, addr string) {
	switch {
	case strings.HasPrefix(host, "unix://"):
		return "unix", strings.TrimPrefix(host, "unix://")
	case strings.HasPrefix(host, "npipe://"):
		return "npipe", strings.TrimPrefix(host, "npipe://")
	case strings.HasPrefix(host, "tcp://"):
		return "tcp", strings.TrimPrefix(host, "tcp://")
	default:
		return "unix", host
	}
}
