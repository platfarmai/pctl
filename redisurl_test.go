package main

import (
	"strings"
	"testing"
)

func TestParseRedisURL(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
		user string
		pass string
		db   int
		ssl  bool
	}{
		{"redis://redis:6379/0", "redis", 6379, "", "", 0, false},
		{"redis://:s3cret@redis:6379/0", "redis", 6379, "", "s3cret", 0, false},
		{"redis://myuser:s3cret@redis:6379/2", "redis", 6379, "myuser", "s3cret", 2, false},
		{"rediss://:pw%40x@cache.example.com:6380/0", "cache.example.com", 6380, "", "pw@x", 0, true},
		{"redis://:p%2Fass@host:6379/1", "host", 6379, "", "p/ass", 1, false},
	}
	for _, tc := range cases {
		c, err := parseRedisURL(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if c.Host != tc.host || c.Port != tc.port || c.Username != tc.user || c.Password != tc.pass || c.Database != tc.db || c.SSL != tc.ssl {
			t.Fatalf("%s: got %+v", tc.in, c)
		}
	}
}

func TestKongRedisInlinePassword(t *testing.T) {
	s := kongRedisInline(redisConn{Host: "redis", Port: 6379, Password: "s3cret", Database: 0})
	for _, part := range []string{`password: "s3cret"`, "host: redis", "port: 6379"} {
		if !strings.Contains(s, part) {
			t.Fatalf("missing %q in %s", part, s)
		}
	}
}
