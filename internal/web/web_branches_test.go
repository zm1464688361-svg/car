package web

import (
	"net/url"
	"strings"
	"testing"
)

// TestHandlerErrorBranches 覆盖各动作处理器的业务失败分支与页面空态/越权分支。
func TestHandlerErrorBranches(t *testing.T) {
	ts := newTestServer(t)
	applicant := login(t, ts, "1")
	approver := login(t, ts, "3")
	operator := login(t, ts, "4")

	// 构造一个已批准活动
	postForm(t, ts, "/actions/create", applicant, url.Values{"reason": {"分支"}, "interval": {"30"}})
	postForm(t, ts, "/actions/submit", applicant, url.Values{"id": {"1"}})

	// 退回：空原因报错；成功退回后页面可见原因
	resp, _ := postForm(t, ts, "/actions/reject", approver, url.Values{"id": {"1"}, "reason": {""}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("空原因退回应报错: %s", resp.Header.Get("Location"))
	}
	resp, _ = postForm(t, ts, "/actions/reject", approver, url.Values{"id": {"1"}, "reason": {"理由"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("退回应成功: %s", resp.Header.Get("Location"))
	}
	resp, body := get(t, ts, "/applicant", applicant)
	if !strings.Contains(body, "理由") {
		t.Fatalf("退回原因应展示")
	}

	// 非法 id / 不存在活动（按各动作要求的角色调用）
	for _, tc := range []struct{ path, cookie string }{
		{"/actions/submit", applicant},
		{"/actions/approve", approver},
		{"/actions/reject", approver},
		{"/actions/complete", approver},
		{"/actions/approve-restore", approver},
		{"/actions/mark-restored", approver},
		{"/actions/pilot", operator},
		{"/actions/expand", operator},
		{"/actions/stop", operator},
		{"/actions/request-restore", operator},
		{"/actions/execute-restore", operator},
	} {
		resp, _ = postForm(t, ts, tc.path, tc.cookie, url.Values{"id": {"abc"}})
		if !strings.Contains(resp.Header.Get("Location"), "err=") {
			t.Fatalf("%s 非法 id 应报错: %s", tc.path, resp.Header.Get("Location"))
		}
		resp, _ = postForm(t, ts, tc.path, tc.cookie, url.Values{"id": {"999"}})
		if !strings.Contains(resp.Header.Get("Location"), "err=") {
			t.Fatalf("%s 不存在活动应报错: %s", tc.path, resp.Header.Get("Location"))
		}
	}

	// 模拟器分支：非法版本、坏令牌、未知车辆
	resp, _ = postForm(t, ts, "/sim/ack", operator, url.Values{"token": {"vtok-V1"}, "version": {"abc"}, "result": {"success"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("非法版本应报错: %s", resp.Header.Get("Location"))
	}
	resp, _ = postForm(t, ts, "/sim/pull", operator, url.Values{"token": {"bad-token"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("坏令牌应报错: %s", resp.Header.Get("Location"))
	}
	resp, _ = postForm(t, ts, "/sim/online", operator, url.Values{"vehicle": {"X9"}, "online": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("未知车辆应报错: %s", resp.Header.Get("Location"))
	}
	resp, _ = postForm(t, ts, "/sim/ack", operator, url.Values{"token": {"vtok-V1"}, "version": {"1"}, "result": {"weird"}})
	if !strings.Contains(resp.Header.Get("Location"), "invalid_result") {
		t.Fatalf("非法结果应提示拒绝: %s", resp.Header.Get("Location"))
	}

	// 核查页：不存在活动 → 错误提示页
	resp, body = get(t, ts, "/activity/999", operator)
	if resp.StatusCode != 200 && resp.StatusCode != 404 && !strings.Contains(body, "err") {
		t.Fatalf("不存在活动核查页异常: %d", resp.StatusCode)
	}

	// 根路径：未登录 → 重定向；登录未知会话 Cookie → 重定向
	resp, _ = get(t, ts, "/", "")
	if resp.StatusCode != 302 {
		t.Fatalf("未登录根路径应重定向: %d", resp.StatusCode)
	}
	resp, _ = get(t, ts, "/applicant", "session=tampered-data")
	if resp.StatusCode != 302 {
		t.Fatalf("无效会话应重定向: %d", resp.StatusCode)
	}

	// 修改草稿（不重新提交）→ 提交
	postForm(t, ts, "/actions/update", applicant, url.Values{"id": {"1"}, "reason": {"分支-改"}, "interval": {"40"}})
	resp, _ = postForm(t, ts, "/actions/submit", applicant, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("提交应成功: %s", resp.Header.Get("Location"))
	}
	// 重复提交 → 报错
	resp, _ = postForm(t, ts, "/actions/submit", applicant, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("重复提交应报错: %s", resp.Header.Get("Location"))
	}

	// 审批页面（待审批列表有内容）
	resp, body = get(t, ts, "/approver", approver)
	if resp.StatusCode != 200 || !strings.Contains(body, "分支-改") {
		t.Fatalf("审批台应显示待审批申请")
	}
	postForm(t, ts, "/actions/approve", approver, url.Values{"id": {"1"}})

	// 运营台：已批准分区应有内容
	resp, body = get(t, ts, "/operator", operator)
	if !strings.Contains(body, "分支-改") {
		t.Fatalf("运营台应显示已批准活动")
	}
	postForm(t, ts, "/actions/pilot", operator, url.Values{"id": {"1"}, "vehicle": []string{"V1", "V3"}})
	resp, body = get(t, ts, "/operator", operator)
	if !strings.Contains(body, "试点中") {
		t.Fatalf("运营台应显示试点中分区")
	}
	// 停止后恢复相关分区
	postForm(t, ts, "/sim/ack", operator, url.Values{"token": {"vtok-V1"}, "version": {"1"}, "result": {"success"}})
	postForm(t, ts, "/actions/stop", operator, url.Values{"id": {"1"}, "reason": {"超时放弃"}})
	resp, body = get(t, ts, "/operator", operator)
	if !strings.Contains(body, "停止") {
		t.Fatalf("运营台应显示停止活动分区")
	}
	// 发起恢复 → 负责人批准 → 执行恢复
	resp, _ = postForm(t, ts, "/actions/request-restore", operator, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("发起恢复失败: %s", resp.Header.Get("Location"))
	}
	// 恢复待批状态执行 → 报错分支
	resp, _ = postForm(t, ts, "/actions/execute-restore", operator, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("恢复待批时执行应报错: %s", resp.Header.Get("Location"))
	}
	resp, _ = postForm(t, ts, "/actions/approve-restore", approver, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("批准恢复失败: %s", resp.Header.Get("Location"))
	}
	resp, _ = postForm(t, ts, "/actions/execute-restore", operator, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "ok=") {
		t.Fatalf("执行恢复失败: %s", resp.Header.Get("Location"))
	}
	// 重复执行恢复 → 报错
	resp, _ = postForm(t, ts, "/actions/execute-restore", operator, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("重复执行恢复应报错: %s", resp.Header.Get("Location"))
	}
	// 记恢复完成（未全部确认 → 报错分支）
	resp, _ = postForm(t, ts, "/actions/mark-restored", approver, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("未全部确认应报错: %s", resp.Header.Get("Location"))
	}
	// 记完成（非推广中 → 报错分支）
	resp, _ = postForm(t, ts, "/actions/complete", approver, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("非推广中记完成应报错: %s", resp.Header.Get("Location"))
	}
	// 推广（非试点中 → 报错分支）
	resp, _ = postForm(t, ts, "/actions/expand", operator, url.Values{"id": {"1"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("非试点中推广应报错: %s", resp.Header.Get("Location"))
	}
	// 停止（已恢复 → 报错分支）
	resp, _ = postForm(t, ts, "/actions/stop", operator, url.Values{"id": {"1"}, "reason": {"x"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("已恢复活动停止应报错: %s", resp.Header.Get("Location"))
	}
	// 试点（非待试点 → 报错分支）
	resp, _ = postForm(t, ts, "/actions/pilot", operator, url.Values{"id": {"1"}, "vehicle": []string{"V1", "V3"}})
	if !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("非待试点发起试点应报错: %s", resp.Header.Get("Location"))
	}
}
