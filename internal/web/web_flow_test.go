package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestFullLifecycleHTTP 通过 HTTP 覆盖全生命周期：创建/修改/提交/退回/批准/试点/推广/停止/恢复/记完成。
func TestFullLifecycleHTTP(t *testing.T) {
	ts := newTestServer(t)
	applicant := login(t, ts, "1")
	approver := login(t, ts, "3")
	operator := login(t, ts, "4")

	// 创建申请（间隔非数字 → 报错重定向）
	resp, _ := postForm(t, ts, "/actions/create", applicant, url.Values{"reason": {"x"}, "interval": {"abc"}})
	if resp.StatusCode != 302 || !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("非法间隔应报错: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// 正常创建 → 修改草稿 → 提交
	postForm(t, ts, "/actions/create", applicant, url.Values{"reason": {"全流程"}, "interval": {"30"}})
	postForm(t, ts, "/actions/update", applicant, url.Values{"id": {"1"}, "reason": {"全流程-改"}, "interval": {"35"}})
	resp, _ = postForm(t, ts, "/actions/submit", applicant, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("提交失败: %s", resp.Header.Get("Location"))
	}

	// 负责人退回 → 申请人修改并重新提交 → 批准
	resp, _ = postForm(t, ts, "/actions/reject", approver, url.Values{"id": {"1"}, "reason": {"间隔不合理"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("退回失败: %s", resp.Header.Get("Location"))
	}
	postForm(t, ts, "/actions/update", applicant, url.Values{"id": {"1"}, "reason": {"全流程-改2"}, "interval": {"30"}, "resubmit": {"1"}})
	postForm(t, ts, "/actions/approve", approver, url.Values{"id": {"1"}})

	// 运营发起试点 → 试点成功 → 推广
	postForm(t, ts, "/actions/pilot", operator, url.Values{"id": {"1"}, "vehicle": []string{"V1", "V3"}})

	// 用模拟器动作接口回执（走表单）
	postForm(t, ts, "/sim/pull", operator, url.Values{"token": {"vtok-V1"}})
	resp, _ = postForm(t, ts, "/sim/ack", operator, url.Values{"token": {"vtok-V1"}, "version": {"1"}, "result": {"success"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("模拟器回执失败: %s", resp.Header.Get("Location"))
	}
	postForm(t, ts, "/sim/ack", operator, url.Values{"token": {"vtok-V3"}, "version": {"1"}, "result": {"success"}})
	postForm(t, ts, "/actions/expand", operator, url.Values{"id": {"1"}})

	// 推广车辆回执成功
	for _, vid := range []string{"V2", "V4", "V5"} {
		postForm(t, ts, "/sim/ack", operator, url.Values{"token": {"vtok-" + vid}, "version": {"1"}, "result": {"success"}})
	}

	// 负责人记完成
	resp, _ = postForm(t, ts, "/actions/complete", approver, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("记完成失败: %s", resp.Header.Get("Location"))
	}

	// 车辆在线切换（模拟器）
	resp, _ = postForm(t, ts, "/sim/online", operator, url.Values{"vehicle": {"V2"}, "online": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("在线切换失败: %s", resp.Header.Get("Location"))
	}

	// 工作台页面
	resp, body := get(t, ts, "/operator", operator)
	if resp.StatusCode != 200 || !strings.Contains(body, "发布运营工作台") {
		t.Fatalf("运营台异常: %d", resp.StatusCode)
	}
	resp, body = get(t, ts, "/approver", approver)
	if resp.StatusCode != 200 {
		t.Fatalf("审批台异常: %d", resp.StatusCode)
	}
	resp, _ = get(t, ts, "/activity/1", approver)
	if resp.StatusCode != 200 {
		t.Fatal("核查页异常")
	}

	// 退出登录
	resp, _ = postForm(t, ts, "/logout", operator, url.Values{})
	if resp.StatusCode != 302 {
		t.Fatalf("退出登录应重定向: %d", resp.StatusCode)
	}
}

// TestSecondActivityStopRestoreHTTP 第二个活动走停止/恢复全流程。
func TestSecondActivityStopRestoreHTTP(t *testing.T) {
	ts := newTestServer(t)
	applicant := login(t, ts, "1")
	approver := login(t, ts, "3")
	operator := login(t, ts, "4")

	postForm(t, ts, "/actions/create", applicant, url.Values{"reason": {"停止恢复"}, "interval": {"30"}})
	postForm(t, ts, "/actions/submit", applicant, url.Values{"id": {"1"}})
	postForm(t, ts, "/actions/approve", approver, url.Values{"id": {"1"}})
	postForm(t, ts, "/actions/pilot", operator, url.Values{"id": {"1"}, "vehicle": []string{"V1", "V3"}})
	postForm(t, ts, "/sim/ack", operator, url.Values{"token": {"vtok-V1"}, "version": {"1"}, "result": {"success"}})

	// 停止（必填原因）→ 发起恢复 → 批准恢复 → 执行恢复
	resp, _ := postForm(t, ts, "/actions/stop", operator, url.Values{"id": {"1"}, "reason": {"方向变更"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("停止失败: %s", resp.Header.Get("Location"))
	}
	resp, _ = postForm(t, ts, "/actions/request-restore", operator, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("发起恢复失败: %s", resp.Header.Get("Location"))
	}
	resp, _ = postForm(t, ts, "/actions/approve-restore", approver, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("批准恢复失败: %s", resp.Header.Get("Location"))
	}
	resp, _ = postForm(t, ts, "/actions/execute-restore", operator, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("执行恢复失败: %s", resp.Header.Get("Location"))
	}

	// 恢复下发回执成功（V1 版本 2）
	postForm(t, ts, "/sim/ack", operator, url.Values{"token": {"vtok-V1"}, "version": {"2"}, "result": {"success"}})
	resp, _ = postForm(t, ts, "/actions/mark-restored", approver, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("仍有未确认项应报错: %s", resp.Header.Get("Location"))
	}
	// V3 也在试点时获得过下发 → 恢复版本 2
	postForm(t, ts, "/sim/ack", operator, url.Values{"token": {"vtok-V3"}, "version": {"2"}, "result": {"success"}})
	resp, _ = postForm(t, ts, "/actions/mark-restored", approver, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("记恢复完成失败: %s", resp.Header.Get("Location"))
	}

	// 模拟器页面与拉取
	resp, simBody := get(t, ts, "/simulator", operator)
	if resp.StatusCode != 200 || !strings.Contains(simBody, "vtok") {
		t.Fatalf("模拟器异常: %d", resp.StatusCode)
	}
	resp, body := get2(t, ts, "/api/vehicle/pull", "X-Vehicle-Token: vtok-V1")
	if resp.StatusCode != 200 {
		t.Fatalf("API 拉取异常: %d %s", resp.StatusCode, body)
	}
}

// get2 允许重复读取响应体（带调试输出）。
func get2(t *testing.T, ts *httptest.Server, path, header string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.Header.Set(header[:strings.Index(header, ":")], strings.TrimSpace(header[strings.Index(header, ":")+1:]))
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// TestLoginInvalidUID 非法账号登录。
func TestLoginInvalidUID(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := postForm(t, ts, "/login", "", url.Values{"uid": {"999"}})
	if resp.StatusCode != 302 || !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("非法账号应报错: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = postForm(t, ts, "/login", "", url.Values{"uid": {"abc"}})
	if resp.StatusCode != 302 || !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("非数字账号应报错: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}
