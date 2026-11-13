package service

import "testing"

// TestRestoreNoBaseline 防御分支：参与过下发的车辆缺少基线值时，执行恢复应拒绝。
func TestRestoreNoBaseline(t *testing.T) {
	s := newTestSvc(t)
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V3"}), "pilot")
	mustOK(t, s.StopActivity(uidWang, id, "x"), "stop")
	mustOK(t, s.RequestRestore(uidWang, id), "request")
	mustOK(t, s.ApproveRestore(uidLi, id), "approve")

	// 人为清除基线，模拟数据异常
	if _, err := s.DB.Exec(`UPDATE vehicles SET baseline=NULL WHERE id='V1'`); err != nil {
		t.Fatal(err)
	}
	expectBiz(t, s.ExecuteRestore(uidWang, id), "no_baseline", "缺少基线")
}
