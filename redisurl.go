package main

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// redisConn is the subset of PF_REDIS_URL we need for Kong rate-limiting.
type redisConn struct {
	Host     string
	Port     int
	Username string
	Password string
	Database int
	SSL      bool
}

func parseRedisURL(raw string) (redisConn, error) {
	c := redisConn{Host: "redis", Port: 6379, Database: 0}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" || strings.EqualFold(raw, "false") {
		return c, fmt.Errorf("empty redis url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return c, err
	}
	switch strings.ToLower(u.Scheme) {
	case "redis":
		c.SSL = false
	case "rediss":
		c.SSL = true
	default:
		return c, fmt.Errorf("unsupported scheme %q (use redis:// or rediss://)", u.Scheme)
	}
	host := u.Hostname()
	if host != "" {
		c.Host = host
	}
	if p := u.Port(); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil {
			return c, err
		}
		c.Port = port
	} else if host != "" && !strings.Contains(u.Host, ":") {
		// url.Parse may leave Host as hostname only
		if h, p, err := net.SplitHostPort(u.Host); err == nil {
			c.Host = h
			if port, err := strconv.Atoi(p); err == nil {
				c.Port = port
			}
		}
	}
	if u.User != nil {
		c.Username = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			c.Password = pw
		}
	}
	path := strings.TrimPrefix(u.Path, "/")
	if path != "" {
		db, err := strconv.Atoi(path)
		if err != nil {
			return c, fmt.Errorf("redis database path: %w", err)
		}
		c.Database = db
	}
	return c, nil
}

// kongRedisInline builds the nested redis: { ... } fragment for rate-limiting (Kong 3.x).
func kongRedisInline(c redisConn) string {
	parts := []string{
		fmt.Sprintf("host: %s", c.Host),
		fmt.Sprintf("port: %d", c.Port),
		"timeout: 2000",
		fmt.Sprintf("database: %d", c.Database),
	}
	if c.SSL {
		parts = append(parts, "ssl: true")
	}
	if c.Username != "" {
		parts = append(parts, fmt.Sprintf("username: %q", c.Username))
	}
	if c.Password != "" {
		parts = append(parts, fmt.Sprintf("password: %q", c.Password))
	}
	return "redis: { " + strings.Join(parts, ", ") + " }"
}
