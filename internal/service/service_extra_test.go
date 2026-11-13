package service

import (
	"testing"
	"time"

	"github.com/zm1464688361-svg/car/internal/model"
)

// TestApproveByUnknownUser 身份不存在时拒绝（防越权兜底）。
func TestApproveByUnknownUser(t *testing.T) {
	s := newTestSvc(t)
	id, _ := s.CreateApplication(uidZhang, "x", 30)
	mustOK(t, s.SubmitApplication(uidZhang, id), "submit")
	expectBiz(t, s.ApproveActivity(999, id), "forbidden", "未知身份审批")
	expectBiz(t, s.RejectActivity(999, id, "r"), "forbidden", "未知身份退回")
}

// TestExpandTimeoutKeepsExpanding 推广阶段超时：仅标记下发超时，活动保持推广中（试点暂停仅针对试点批次）。
func TestExpandTimeoutKeepsExpanding(t *testing.T) {
	s := newTestSvc(t)
	base := time.Now().UTC().Truncate(time.Second)
	s.Now = func() time.Time { return base }
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V3"}), "pilot")
	v1 := ackVersion(t, s, "V1")
	ackAll(t, s, []string{"V1"}, v1, model.AckSuccess)
	v3 := ackVersion(t, s, "V3")
	ackAll(t, s, []string{"V3"}, v3, model.AckSuccess)
	mustOK(t, s.ConfirmExpand(uidWang, id), "expand")

	// 推广车超时
	s.Now = func() time.Time { return base.Add(61 * time.Second) }
	n, err := s.MarkTimeouts()
	mustOK(t, err, "mark")
	if n != 3 {
		t.Fatalf("应标记 3 条推广超时，实际 %d", n)
	}
	a, _ := s.GetActivity(id)
	if a.Status != model.ActExpanding {
		t.Fatalf("推广超时不应改变活动状态，实际 %s", a.Status)
	}
	// 停止仍可由负责人执行
	mustOK(t, s.StopActivity(uidLi, id, "超时放弃"), "stop by approver")
	a, _ = s.GetActivity(id)
	if a.Status != model.ActStopped {
		t.Fatalf("负责人停止失败: %s", a.Status)
	}
}

// TestStartTimeoutTicker 后台教学时钟可启动与停止（时间按秒截断，故用 1 秒超时）。
func TestStartTimeoutTicker(t *testing.T) {
	s := newTestSvc(t)
	s.Timeout = 1 * time.Second
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V3"}), "pilot")
	stop := s.StartTimeoutTicker(100 * time.Millisecond)
	time.Sleep(1500 * time.Millisecond)
	close(stop)
	d, err := s.GetActivityDetail(id)
	mustOK(t, err, "detail")
	for _, p := range d.Publishes {
		if p.Status != model.PubTimeout {
			t.Fatalf("时钟应触发超时: %+v", p)
		}
	}
}

// TestUpdateDraftSave 草稿保存（不重新提交）。
func TestUpdateDraftSave(t *testing.T) {
	s := newTestSvc(t)
	id, _ := s.CreateApplication(uidZhang, "初稿", 30)
	mustOK(t, s.UpdateApplication(uidZhang, id, "改稿", 45, false), "update draft")
	a, _ := s.GetActivity(id)
	if a.Status != model.ActDraft || a.Interval != 45 {
		t.Fatalf("草稿保存异常: %+v", a)
	}
}
