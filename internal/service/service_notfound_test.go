package service

import "testing"

// TestNotFoundPaths 全部以不存在活动/车辆为入参，覆盖 not_found 分支与事务回滚路径。
func TestNotFoundPaths(t *testing.T) {
	s := newTestSvc(t)
	expectBiz(t, s.UpdateApplication(uidZhang, 999, "x", 30, true), "not_found", "修改不存在申请")
	expectBiz(t, s.SubmitApplication(uidZhang, 999), "not_found", "提交不存在申请")
	expectBiz(t, s.ApproveActivity(uidLi, 999), "not_found", "批准不存在活动")
	expectBiz(t, s.RejectActivity(uidLi, 999, "r"), "not_found", "退回不存在活动")
	expectBiz(t, s.StartPilot(uidWang, 999, []string{"V1", "V3"}), "not_found", "试点不存在活动")
	expectBiz(t, s.ConfirmExpand(uidWang, 999), "not_found", "推广不存在活动")
	expectBiz(t, s.StopActivity(uidWang, 999, "r"), "not_found", "停止不存在活动")
	expectBiz(t, s.RequestRestore(uidWang, 999), "not_found", "恢复不存在活动")
	expectBiz(t, s.ApproveRestore(uidLi, 999), "not_found", "批准恢复不存在活动")
	expectBiz(t, s.ExecuteRestore(uidWang, 999), "not_found", "执行恢复不存在活动")
	expectBiz(t, s.MarkRestored(uidLi, 999), "not_found", "记恢复不存在活动")
	expectBiz(t, s.CompleteActivity(uidLi, 999), "not_found", "记完成不存在活动")
	expectBiz(t, s.SetVehicleOnline("X9", true), "not_found", "切换不存在车辆")
}

// TestStopOnUnknownVehicleID 试点车辆校验：车辆必须属于车队。
func TestStopOnUnknownVehicleID(t *testing.T) {
	s := newTestSvc(t)
	id := createApproved(t, s)
	expectBiz(t, s.StartPilot(uidWang, id, []string{"V1", "XX"}), "bad_pilot", "未知车辆")
	expectBiz(t, s.StartPilot(uidWang, id, []string{"V1", "V1"}), "bad_pilot", "重复车辆")
}
