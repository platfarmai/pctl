package main

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

//go:embed console.html
var consoleHTML []byte

// runServe：svc-console —— pctl 的 Web 外壳（附录 H.6 最小版）。
// 只读投影 + 既有容器启停；写操作全部经 pctl 内部函数落文件，不产生第二真相源。
func runServe(root string) error {
	pub, err := loadPublicKey()
	if err != nil {
		return err
	}
	s := &console{root: root, pub: pub, authURL: envOr("AUTH_URL", "http://auth:8080")}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(consoleHTML)
	})
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /api/services", s.admin(s.handleServices))
	mux.HandleFunc("POST /api/services/{id}/toggle", s.admin(s.handleToggle))
	mux.HandleFunc("GET /api/services/{id}/logs", s.admin(s.handleLogs))
	mux.HandleFunc("GET /api/audit", s.admin(s.handleAudit))

	fmt.Println("pctl console listening on :8080")
	return http.ListenAndServe(":8080", mux)
}

type console struct {
	root    string
	pub     *rsa.PublicKey
	authURL string
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func loadPublicKey() (*rsa.PublicKey, error) {
	path := envOr("JWT_PUBLIC_KEY_FILE", "/pf/jwt.pub")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("invalid public key PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pk, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}
	return pk, nil
}

type consoleClaims struct {
	TokenType string `json:"tokenType"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	jwt.RegisteredClaims
}

// admin 中间件：RS256 验签 + access + role=admin（附录 H.6：console 全路由 admin-only）。
func (s *console) admin(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			jsonErr(w, 401, "missing bearer token")
			return
		}
		claims := &consoleClaims{}
		tok, err := jwt.ParseWithClaims(strings.TrimPrefix(h, "Bearer "), claims,
			func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
					return nil, errors.New("unexpected signing method")
				}
				return s.pub, nil
			}, jwt.WithIssuer("pf-auth"))
		if err != nil || !tok.Valid || claims.TokenType != "access" {
			jsonErr(w, 401, "invalid token")
			return
		}
		if claims.Role != "admin" {
			jsonErr(w, 403, "admin role required")
			return
		}
		next(w, r, claims.Username)
	}
}

func (s *console) handleLogin(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	resp, err := http.Post(s.authURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		jsonErr(w, 502, "auth unreachable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *console) handleServices(w http.ResponseWriter, _ *http.Request, _ string) {
	manifests, err := loadManifests(s.root)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	containers, cerr := listProjectContainers()
	type row struct {
		ID, Mount, Trust, Lang, State, Status string
		Enabled                               bool
		Rate                                  int
	}
	var out []row
	for _, m := range manifests {
		trust := m.Trust
		if trust == "" {
			trust = "first-party"
		}
		state, status := "unknown", "docker api unavailable"
		if cerr == nil {
			if c, ok := containers[m.ID]; ok {
				state, status = c.State, c.Status
			} else {
				state, status = "absent", "容器未创建"
			}
		}
		out = append(out, row{m.ID, m.Mount.Path, trust, m.Lang, state, status,
			m.IsEnabled(), m.Limits.RatePerMinute})
	}
	writeJSONResp(w, out)
}

func (s *console) handleToggle(w http.ResponseWriter, r *http.Request, user string) {
	id := r.PathValue("id")
	var in struct{ Enable bool }
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		jsonErr(w, 400, "body {enable: bool} required")
		return
	}
	marker := filepath.Join(s.root, "services", id, ".disabled")
	if _, err := os.Stat(filepath.Dir(marker)); err != nil {
		jsonErr(w, 404, "service not found")
		return
	}
	if in.Enable {
		_ = os.Remove(marker)
	} else if err := os.WriteFile(marker, []byte("disabled by console\n"), 0o644); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if err := runSync(s.root); err != nil { // 真相源仍是文件：console 只是改清单状态 + 重新生成
		jsonErr(w, 500, "sync: "+err.Error())
		return
	}
	action := "stop"
	if in.Enable {
		action = "start"
	}
	if err := containerAction(id, action); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if err := containerAction("gateway", "restart"); err != nil {
		jsonErr(w, 500, "gateway restart: "+err.Error())
		return
	}
	s.audit(user, action, id)
	writeJSONResp(w, map[string]bool{"ok": true})
}

func (s *console) handleLogs(w http.ResponseWriter, r *http.Request, _ string) {
	logs, err := containerLogs(r.PathValue("id"), 100)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	writeJSONResp(w, map[string]string{"logs": logs})
}

func (s *console) handleAudit(w http.ResponseWriter, _ *http.Request, _ string) {
	raw, _ := os.ReadFile(filepath.Join(s.root, ".console-audit.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	writeJSONResp(w, lines)
}

func (s *console) audit(user, action, target string) {
	entry, _ := json.Marshal(map[string]string{
		"ts": time.Now().Format(time.RFC3339), "user": user, "action": action, "target": target,
	})
	f, err := os.OpenFile(filepath.Join(s.root, ".console-audit.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(entry, '\n'))
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSONResp(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
