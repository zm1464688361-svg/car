package service

import (
	"testing"

	"github.com/zm1464688361-svg/car/internal/model"
)

// TestFaultInjection 通过删除表结构注入底层 SQL 错误，覆盖各方法的错误处理分支。
func TestFaultInjection(t *testing.T) {
	t.Run("drop_activities", func(t *testing.T) {
		s := newTestSvc(t)
		mustDrop(t, s, "activities")
		expectRawErr(t, s.UpdateApplication(uidZhang, 1, "x", 30, true), "update")
		expectRawErr(t, s.SubmitApplication(uidZhang, 1), "submit")
		expectRawErr(t, s.ApproveActivity(uidLi, 1), "approve")
		expectRawErr(t, s.RejectActivity(uidLi, 1, "r"), "reject")
		expectRawErr(t, s.StartPilot(uidWang, 1, []string{"V1", "V3"}), "pilot")
		expectRawErr(t, s.ConfirmExpand(uidWang, 1), "expand")
		expectRawErr(t, s.StopActivity(uidWang, 1, "r"), "stop")
		expectRawErr(t, s.RequestRestore(uidWang, 1), "request")
		expectRawErr(t, s.ApproveRestore(uidLi, 1), "approve restore")
		expectRawErr(t, s.ExecuteRestore(uidWang, 1), "execute restore")
		expectRawErr(t, s.MarkRestored(uidLi, 1), "mark restored")
		expectRawErr(t, s.CompleteActivity(uidLi, 1), "complete")
		_, err := s.ListActivities(0)
		expectRawErr(t, err, "list")
		_, err = s.GetActivity(1)
		expectRawErr(t, err, "get")
		_, err = s.GetActivityDetail(1)
		expectRawErr(t, err, "detail")
	})

	t.Run("drop_vehicles", func(t *testing.T) {
		s := newTestSvc(t)
		mustDrop(t, s, "vehicles")
		_, err := s.ListVehicles()
		expectRawErr(t, err, "list vehicles")
		expectRawErr(t, s.SetVehicleOnline("V1", true), "set online")
		_, err = s.VehiclePull("vtok-V1")
		expectRawErr(t, err, "pull")
		_, err = s.VehicleAck("vtok-V1", 1, model.AckSuccess)
		expectRawErr(t, err, "ack")
		mustDrop(t, s, "publishes")
		_, err = s.MarkTimeouts()
		expectRawErr(t, err, "mark timeouts")
		mustDrop(t, s, "audits")
		expectRawErr(t, s.StartPilot(uidWang, 1, []string{"V1", "V3"}), "pilot with no vehicles")
	})

	t.Run("drop_publishes", func(t *testing.T) {
		s := newTestSvc(t)
		id := createApproved(t, s)
		mustDrop(t, s, "publishes")
		expectRawErr(t, s.StartPilot(uidWang, id, []string{"V1", "V3"}), "pilot")
		expectRawErr(t, s.ConfirmExpand(uidWang, id), "expand")
		_, err := s.VehiclePull("vtok-V1")
		expectRawErr(t, err, "pull")
	})

	t.Run("drop_audits", func(t *testing.T) {
		s := newTestSvc(t)
		id := createApproved(t, s)
		mustDrop(t, s, "audits")
		expectRawErr(t, s.ApproveActivity(uidLi, id), "approve audit err")
	})

	t.Run("drop_users", func(t *testing.T) {
		s := newTestSvc(t)
		id := createApproved(t, s)
		mustDrop(t, s, "users")
		_, err := s.ListUsers()
		expectRawErr(t, err, "list users")
		_, err = s.GetUser(uidLi)
		expectRawErr(t, err, "get user")
		expectRawErr(t, s.ApproveActivity(uidLi, id), "approve role err")
	})

	t.Run("drop_receipts", func(t *testing.T) {
		s := newTestSvc(t)
		id := createApproved(t, s)
		mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V3"}), "pilot")
		mustDrop(t, s, "receipts")
		_, err := s.VehicleAck("vtok-V1", 1, model.AckSuccess)
		expectRawErr(t, err, "ack save receipt")
		_, err = s.ListReceipts()
		expectRawErr(t, err, "list receipts")
	})
}

func mustDrop(t *testing.T, s *Service, table string) {
	t.Helper()
	if _, err := s.DB.Exec("DROP TABLE IF EXISTS " + table); err != nil {
		t.Fatalf("drop %s: %v", table, err)
	}
}

// expectRawErr 期望返回底层 SQL 错误（非业务错误码断言，只要出错即可）。
func expectRawErr(t *testing.T, err error, context string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: 底层故障应返回错误", context)
	}
}
