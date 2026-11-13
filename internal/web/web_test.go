package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm1464688361-svg/car/internal/service"
	"github.com/zm1464688361-svg/car/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(db); err != nil {
		t.Fatal(err)
	}
	svc := service.New(db)
	svc.Timeout = 10 * time.Second
	s, err := NewServer(svc, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// noRedirect 不跟随重定向的客户端（便于断言 302 与中间响应 Cookie）。
var noRedirect = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// login 以指定账号登录并返回会话 Cookie。
func login(t *testing.T, ts *httptest.Server, uid string) string {
	t.Helper()
	form := url.Values{"uid": {uid}}
	resp, err := noRedirect.PostForm(ts.URL+"/login", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "session" {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatal("未获得会话 Cookie")
	return ""
}

func get(t *testing.T, ts *httptest.Server, path, cookie string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func postForm(t *testing.T, ts *httptest.Server, path, cookie string, form url.Values) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestLoginPage(t *testing.T) {
	ts := newTestServer(t)
	resp, body := get(t, ts, "/login", "")
	if resp.StatusCode != 200 || !strings.Contains(body, "张研发") {
		t.Fatalf("登录页异常: %d", resp.StatusCode)
	}
	// 未登录访问工作台 → 重定向登录页
	resp, _ = get(t, ts, "/applicant", "")
	if resp.StatusCode != 302 {
		t.Fatalf("未登录应重定向: %d", resp.StatusCode)
	}
}

func TestRoleGuard(t *testing.T) {
	ts := newTestServer(t)
	applicant := login(t, ts, "1")
	// 申请人不能访问审批/运营/模拟器页面
	resp, body := get(t, ts, "/approver", applicant)
	if resp.StatusCode != 403 || !strings.Contains(body, "无权访问") {
		t.Fatalf("越权应 403: %d %s", resp.StatusCode, body)
	}
	resp, _ = get(t, ts, "/simulator", applicant)
	if resp.StatusCode != 403 {
		t.Fatalf("越权模拟器应 403: %d", resp.StatusCode)
	}
}

func TestApplicantFlowThroughHTTP(t *testing.T) {
	ts := newTestServer(t)
	applicant := login(t, ts, "1")
	approver := login(t, ts, "3")

	// 创建申请
	resp, body := postForm(t, ts, "/actions/create", applicant,
		url.Values{"reason": {"HTTP 排查"}, "interval": {"25"}})
	if resp.StatusCode != 302 {
		t.Fatalf("创建申请失败: %d %s", resp.StatusCode, body)
	}
	// 间隔非法
	resp, body = postForm(t, ts, "/actions/create", applicant,
		url.Values{"reason": {"x"}, "interval": {"999"}})
	if resp.StatusCode != 302 || !strings.Contains(body, "bad_interval") && !strings.Contains(resp.Header.Get("Location"), "err") {
		t.Fatalf("非法间隔应失败: %d %s %s", resp.StatusCode, body, resp.Header.Get("Location"))
	}

	// 申请人页面可见申请
	resp, body = get(t, ts, "/applicant", applicant)
	if resp.StatusCode != 200 || !strings.Contains(body, "HTTP 排查") {
		t.Fatalf("申请人页面异常: %d", resp.StatusCode)
	}

	// 提交、批准
	postForm(t, ts, "/actions/submit", applicant, url.Values{"id": {"1"}})
	resp, _ = postForm(t, ts, "/actions/approve", approver, url.Values{"id": {"1"}})
	if resp.StatusCode != 302 || !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("批准失败: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// 越权：申请人调审批动作 → 403
	resp, _ = postForm(t, ts, "/actions/approve", applicant, url.Values{"id": {"1"}})
	if resp.StatusCode != 403 {
		t.Fatalf("越权审批应 403: %d", resp.StatusCode)
	}

	// 重复批准 → 重定向携带 err 提示（URL 编码的失败原因）
	resp, _ = postForm(t, ts, "/actions/approve", approver, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("重复批准应携带错误提示: %s", resp.Header.Get("Location"))
	}
}

func TestVehicleAPI(t *testing.T) {
	ts := newTestServer(t)
	operator := login(t, ts, "4")
	approver := login(t, ts, "3")
	applicant := login(t, ts, "1")

	// 完整业务流：创建→提交→批准→试点V1/V3
	postForm(t, ts, "/actions/create", applicant, url.Values{"reason": {"api"}, "interval": {"30"}})
	postForm(t, ts, "/actions/submit", applicant, url.Values{"id": {"1"}})
	postForm(t, ts, "/actions/approve", approver, url.Values{"id": {"1"}})
	resp, body := postForm(t, ts, "/actions/pilot", operator, url.Values{"id": {"1"}, "vehicle": {"V1", "V3"}})
	if resp.StatusCode != 302 {
		t.Fatalf("试点失败: %d %s", resp.StatusCode, body)
	}

	// 车端 API：拉取
	req, _ := http.NewRequest("GET", ts.URL+"/api/vehicle/pull", nil)
	req.Header.Set("X-Vehicle-Token", "vtok-V1")
	resp2, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var pull struct {
		Version        int `json:"version"`
		TargetInterval int `json:"target_interval"`
	}
	b, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if err := json.Unmarshal(b, &pull); err != nil || pull.Version != 1 || pull.TargetInterval != 30 {
		t.Fatalf("拉取异常: %s %s", b, err)
	}

	// 车端 API：回执成功
	payload := `{"version":1,"result":"success"}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/vehicle/ack", strings.NewReader(payload))
	req.Header.Set("X-Vehicle-Token", "vtok-V1")
	req.Header.Set("Content-Type", "application/json")
	resp3, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp3.Body)
	resp3.Body.Close()
	var ack struct {
		Accepted bool   `json:"accepted"`
		Code     string `json:"code"`
	}
	if err := json.Unmarshal(b, &ack); err != nil || !ack.Accepted {
		t.Fatalf("回执异常: %s", b)
	}

	// 缺少令牌
	req, _ = http.NewRequest("GET", ts.URL+"/api/vehicle/pull", nil)
	resp4, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != 401 {
		t.Fatalf("缺令牌应 401: %d", resp4.StatusCode)
	}

	// 非法 JSON
	req, _ = http.NewRequest("POST", ts.URL+"/api/vehicle/ack", strings.NewReader("not-json"))
	req.Header.Set("X-Vehicle-Token", "vtok-V1")
	resp5, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp5.Body.Close()
	if resp5.StatusCode != 400 {
		t.Fatalf("非法 JSON 应 400: %d", resp5.StatusCode)
	}

	// 模拟器页面
	resp6, body := get(t, ts, "/simulator", operator)
	if resp6.StatusCode != 200 || !strings.Contains(body, "vtok-V1") {
		t.Fatalf("模拟器页面异常: %d", resp6.StatusCode)
	}

	// 逐车核查页
	resp7, body := get(t, ts, "/activity/1", operator)
	if resp7.StatusCode != 200 || !strings.Contains(body, "逐车下发") {
		t.Fatalf("核查页异常: %d %s", resp7.StatusCode, body)
	}

	// 根路径跳转
	resp8, _ := get(t, ts, "/", operator)
	if resp8.StatusCode != 302 {
		t.Fatalf("根路径应跳转: %d", resp8.StatusCode)
	}
}

func TestStaticCSS(t *testing.T) {
	ts := newTestServer(t)
	resp, body := get(t, ts, "/static/style.css", "")
	if resp.StatusCode != 200 || !strings.Contains(body, "FLEET") {
		t.Fatalf("静态资源异常: %d", resp.StatusCode)
	}
	resp2, _ := get(t, ts, "/static/nope.css", "")
	if resp2.StatusCode != 404 {
		t.Fatalf("缺失静态文件应 404: %d", resp2.StatusCode)
	}
}
