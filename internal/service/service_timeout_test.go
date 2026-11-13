package service

import (
	"testing"
	"time"

	"github.com/zm1464688361-svg/car/internal/model"
)

// TestAckFailedAfterTimeout 超时后的失败回执：仅记录，不改变结论。
func TestAckFailedAfterTimeout(t *testing.T) {
	s := newTestSvc(t)
	base := time.Now().UTC().Truncate(time.Second)
	s.Now = func() time.Time { return base }
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V3"}), "pilot")

	// 推进时间触发超时
	s.Now = func() time.Time { return base.Add(61 * time.Second) }
	n, err := s.MarkTimeouts()
	mustOK(t, err, "mark")
	if n != 2 {
		t.Fatalf("应标记 2 条超时，实际 %d", n)
	}

	// 已成功的下发不再重复标记：先让 V1 成功再超时判定（跳过分支）
	// 失败回执：该版本已超时 → 仅记录
	rec, err := s.VehicleAck("vtok-V3", 1, model.AckFailed)
	mustOK(t, err, "failed after timeout")
	if rec.Accepted || rec.Code != "already_final" {
		t.Fatalf("超时后失败应仅记录: %+v", rec)
	}
	// 活动保持试点暂停
	a, _ := s.GetActivity(id)
	if a.Status != model.ActPilotPaused {
		t.Fatalf("超时后活动应暂停，实际 %s", a.Status)
	}
}

// TestTimeoutSkipsAckedPublishes 已有结论（成功）的下发不参与超时判定。
func TestTimeoutSkipsAckedPublishes(t *testing.T) {
	s := newTestSvc(t)
	base := time.Now().UTC().Truncate(time.Second)
	s.Now = func() time.Time { return base }
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V3"}), "pilot")
	v1 := ackVersion(t, s, "V1")
	ackAll(t, s, []string{"V1"}, v1, model.AckSuccess)

	s.Now = func() time.Time { return base.Add(61 * time.Second) }
	n, err := s.MarkTimeouts()
	mustOK(t, err, "mark")
	if n != 1 {
		t.Fatalf("仅未回执的 1 条应超时，实际 %d", n)
	}
	d, _ := s.GetActivityDetail(id)
	for _, p := range d.Publishes {
		if p.VehicleID == "V1" && p.Status != model.PubSuccess {
			t.Fatalf("已成功下发不应被改写: %+v", p)
		}
		if p.VehicleID == "V3" && p.Status != model.PubTimeout {
			t.Fatalf("未回执下发应超时: %+v", p)
		}
	}
}
