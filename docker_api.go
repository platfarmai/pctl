package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// 极简 Docker Engine API 客户端（unix socket 直连，避免容器内 compose CLI 的路径映射问题）。
// console 只做既有容器的启停/重启/日志/状态——创建类操作仍归宿主机 pctl CLI。

var dockerHTTP = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			network, addr := engineSocket()
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	},
}

func composeProject() string {
	if v := os.Getenv("PF_COMPOSE_PROJECT"); v != "" {
		return v
	}
	return "platfarm"
}

type containerInfo struct {
	ID      string            `json:"Id"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Labels  map[string]string `json:"Labels"`
	Service string            `json:"-"`
}

// listProjectContainers 返回本 compose 项目全部容器，按 compose service 名索引。
func listProjectContainers() (map[string]containerInfo, error) {
	filters := fmt.Sprintf(`{"label":["com.docker.compose.project=%s"]}`, composeProject())
	resp, err := dockerHTTP.Get("http://docker/v1.43/containers/json?all=true&filters=" + url.QueryEscape(filters))
	if err != nil {
		return nil, fmt.Errorf("docker api: %w", err)
	}
	defer resp.Body.Close()
	var list []containerInfo
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	out := map[string]containerInfo{}
	for _, c := range list {
		c.Service = c.Labels["com.docker.compose.service"]
		out[c.Service] = c
	}
	return out, nil
}

func containerAction(service, action string) error {
	containers, err := listProjectContainers()
	if err != nil {
		return err
	}
	c, ok := containers[service]
	if !ok {
		return fmt.Errorf("容器不存在（宿主机执行 docker compose up -d 后重试）")
	}
	resp, err := dockerHTTP.Post(
		fmt.Sprintf("http://docker/v1.43/containers/%s/%s?t=5", c.ID, action), "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != 304 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("docker %s: %s", action, strings.TrimSpace(string(raw)))
	}
	return nil
}

// containerLogs 取容器日志并解多路复用帧（非 TTY 流带 8 字节头）。
func containerLogs(service string, tail int) (string, error) {
	containers, err := listProjectContainers()
	if err != nil {
		return "", err
	}
	c, ok := containers[service]
	if !ok {
		return "", fmt.Errorf("容器不存在")
	}
	resp, err := dockerHTTP.Get(fmt.Sprintf(
		"http://docker/v1.43/containers/%s/logs?stdout=1&stderr=1&tail=%d", c.ID, tail))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for len(raw) >= 8 {
		size := int(binary.BigEndian.Uint32(raw[4:8]))
		if len(raw) < 8+size {
			break
		}
		b.Write(raw[8 : 8+size])
		raw = raw[8+size:]
	}
	if b.Len() == 0 { // TTY 容器无帧头
		return string(raw), nil
	}
	return b.String(), nil
}
