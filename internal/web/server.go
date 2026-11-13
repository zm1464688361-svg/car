// Package web 提供服务端渲染页面（后端完成前端）与车端模拟 API。
package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zm1464688361-svg/car/internal/model"
	"github.com/zm1464688361-svg/car/internal/service"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server HTTP 服务。
type Server struct {
	svc    *service.Service
	secret []byte
	mux    *http.ServeMux
	tpl    map[string]*template.Template
}

func sign(s *Server, v string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(v))
	return v + "." + hex.EncodeToString(m.Sum(nil))[:16]
}

func unsign(s *Server, v string) (string, bool) {
	i := strings.LastIndex(v, ".")
	if i < 0 || len(v)-i-1 != 16 {
		return "", false
	}
	val, mac := v[:i], v[i+1:]
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(val))
	want := hex.EncodeToString(m.Sum(nil))[:16]
	if !hmac.Equal([]byte(mac), []byte(want)) {
		return "", false
	}
	return val, true
}

// currentUser 从会话 Cookie 解析身份（身份只来自服务端签名 Cookie，防止改参数越权）。
func (s *Server) currentUser(r *http.Request) *model.User {
	c, err := r.Cookie("session")
	if err != nil {
		return nil
	}
	val, ok := unsign(s, c.Value)
	if !ok {
		return nil
	}
	id, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return nil
	}
	u, err := s.svc.GetUser(id)
	if err != nil {
		return nil
	}
	return u
}

func (s *Server) setSession(w http.ResponseWriter, uid int64) {
	val := sign(s, strconv.FormatInt(uid, 10))
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: val, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(12 * time.Hour / time.Second),
	})
}

// requireRole 校验登录与角色；未登录重定向登录页，角色不符返回 403。
func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, roles ...model.Role) *model.User {
	u := s.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return nil
	}
	if len(roles) > 0 {
		ok := false
		for _, ro := range roles {
			if u.Role == ro {
				ok = true
			}
		}
		if !ok {
			http.Error(w, "403 无权访问：当前身份 "+u.Role.Label(), http.StatusForbidden)
			return nil
		}
	}
	return u
}

// flash 提示信息（经查询参数传递，页面展示失败原因）。
type flash struct{ OK, Err string }

func getFlash(r *http.Request) flash {
	return flash{OK: r.URL.Query().Get("ok"), Err: r.URL.Query().Get("err")}
}

func redirectFlash(w http.ResponseWriter, r *http.Request, to, okMsg, errMsg string) {
	sep := "?"
	if strings.Contains(to, "?") {
		sep = "&"
	}
	if okMsg != "" {
		to += sep + "ok=" + okMsg
	} else if errMsg != "" {
		to += sep + "err=" + errMsg
	}
	http.Redirect(w, r, to, http.StatusFound)
}

// bizErr 把业务错误转为页面可读提示。
func bizMsg(err error) string {
	var be *model.BusinessError
	switch {
	case err == nil:
		return ""
	case errorsAs(err, &be):
		return be.Msg
	default:
		return "系统错误：" + err.Error()
	}
}

// errorsAs 小包装避免每个 handler 都引 errors 包。
func errorsAs(err error, target any) bool {
	be, ok := err.(*model.BusinessError)
	if ok {
		if t, ok2 := target.(**model.BusinessError); ok2 {
			*t = be
			return true
		}
	}
	return false
}

// formInt 解析表单整数。
func formInt(r *http.Request, key string) (int, error) {
	v := strings.TrimSpace(r.FormValue(key))
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, model.Err("bad_input", key+" 必须是整数")
	}
	return n, nil
}

func formInt64(r *http.Request, key string) (int64, error) {
	v := strings.TrimSpace(r.FormValue(key))
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, model.Err("bad_input", key+" 必须是整数")
	}
	return n, nil
}

// NewServer 构造服务与路由。
func NewServer(svc *service.Service, sessionSecret string) (*Server, error) {
	s := &Server{svc: svc, secret: []byte(sessionSecret)}
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", s.handleRoot)
	m.HandleFunc("GET /login", s.handleLoginPage)
	m.HandleFunc("POST /login", s.handleLoginPost)
	m.HandleFunc("POST /logout", s.handleLogout)

	m.HandleFunc("GET /applicant", s.handleApplicant)
	m.HandleFunc("GET /approver", s.handleApprover)
	m.HandleFunc("GET /operator", s.handleOperator)
	m.HandleFunc("GET /activity/{id}", s.handleActivity)
	m.HandleFunc("GET /simulator", s.handleSimulator)

	// 业务动作（POST）
	m.HandleFunc("POST /actions/create", s.handleCreate)
	m.HandleFunc("POST /actions/update", s.handleUpdate)
	m.HandleFunc("POST /actions/submit", s.handleSubmit)
	m.HandleFunc("POST /actions/approve", s.handleApprove)
	m.HandleFunc("POST /actions/reject", s.handleReject)
	m.HandleFunc("POST /actions/pilot", s.handlePilot)
	m.HandleFunc("POST /actions/expand", s.handleExpand)
	m.HandleFunc("POST /actions/stop", s.handleStop)
	m.HandleFunc("POST /actions/complete", s.handleComplete)
	m.HandleFunc("POST /actions/request-restore", s.handleRequestRestore)
	m.HandleFunc("POST /actions/approve-restore", s.handleApproveRestore)
	m.HandleFunc("POST /actions/execute-restore", s.handleExecuteRestore)
	m.HandleFunc("POST /actions/mark-restored", s.handleMarkRestored)

	// 车辆模拟器
	m.HandleFunc("POST /sim/online", s.handleSimOnline)
	m.HandleFunc("POST /sim/pull", s.handleSimPull)
	m.HandleFunc("POST /sim/ack", s.handleSimAck)

	// 车端模拟 API（供 curl/测试触发成功、失败、超时、重复、跨车场景）
	m.HandleFunc("POST /api/vehicle/ack", s.handleAPIAck)
	m.HandleFunc("GET /api/vehicle/pull", s.handleAPIPull)

	m.Handle("GET /static/", http.FileServer(http.FS(staticFS)))
	s.mux = m
	return s, nil
}

// Handler 返回根处理器。
func (s *Server) Handler() http.Handler { return s.mux }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
