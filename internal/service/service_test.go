package service

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/zm1464688361-svg/car/internal/model"
	"github.com/zm1464688361-svg/car/internal/store"
)

// newTestSvc 构造内存数据库测试服务。
func newTestSvc(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1) // 内存库必须单连接，否则并发时各连接各自建库
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.Seed(db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return New(db)
}

const (
	uidZhang = int64(1) // 张研发 applicant
	uidZhao  = int64(2) // 赵研发 applicant
	uidLi    = int64(3) // 李负责 approver
	uidWang  = int64(4) // 王运营 operator
)

func expectBiz(t *testing.T, err error, code string, context string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: 期望业务错误 %s，实际成功", context, code)
	}
	var be *model.BusinessError
	if !errors.As(err, &be) {
		t.Fatalf("%s: 期望业务错误，实际 %v", context, err)
	}
	if be.Code != code {
		t.Fatalf("%s: 期望错误码 %s，实际 %s (%s)", context, code, be.Code, be.Msg)
	}
}

func mustOK(t *testing.T, err error, context string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 意外错误 %v", context, err)
	}
}

// createApproved 建立一个已批准、间隔 30s 的活动。
func createApproved(t *testing.T, s *Service) int64 {
	t.Helper()
	id, err := s.CreateApplication(uidZhang, "排查V3诊断码", 30)
	mustOK(t, err, "create")
	mustOK(t, s.SubmitApplication(uidZhang, id), "submit")
	mustOK(t, s.ApproveActivity(uidLi, id), "approve")
	return id
}

func ackAll(t *testing.T, s *Service, vids []string, version int, result string) {
	t.Helper()
	for _, v := range vids {
		rec, err := s.VehicleAck("vtok-"+v, version, result)
		mustOK(t, err, "ack "+v)
		if !rec.Accepted {
			t.Fatalf("ack %s 被拒绝: %s", v, rec.Note)
		}
	}
}

func ackVersion(t *testing.T, s *Service, vid string) int {
	t.Helper()
	p, err := s.VehiclePull("vtok-" + vid)
	mustOK(t, err, "pull "+vid)
	return p.Version
}

// ---------- 申请与审批 ----------

func TestApplicationValidation(t *testing.T) {
	s := newTestSvc(t)

	// 原因必填
	_, err := s.CreateApplication(uidZhang, "", 30)
	expectBiz(t, err, "bad_reason", "空原因")

	// 间隔边界 10~300
	_, err = s.CreateApplication(uidZhang, "x", 9)
	expectBiz(t, err, "bad_interval", "间隔下限")
	_, err = s.CreateApplication(uidZhang, "x", 301)
	expectBiz(t, err, "bad_interval", "间隔上限")
	_, err = s.CreateApplication(uidZhang, "x", 10)
	mustOK(t, err, "间隔下界10")
	_, err = s.CreateApplication(uidZhang, "x", 300)
	mustOK(t, err, "间隔上界300")

	// 非本人不可修改/提交
	id, err := s.CreateApplication(uidZhang, "排查", 30)
	mustOK(t, err, "create")
	expectBiz(t, s.UpdateApplication(uidZhao, id, "改", 40, false), "forbidden", "他人修改")
	expectBiz(t, s.SubmitApplication(uidZhao, id), "forbidden", "他人提交")

	// 重复提交（已 submitted 再提交）
	mustOK(t, s.SubmitApplication(uidZhang, id), "submit")
	expectBiz(t, s.SubmitApplication(uidZhang, id), "bad_state", "重复提交")

	// 自审禁止
	expectBiz(t, s.ApproveActivity(uidZhang, id), "forbidden", "自审")
	// 运营/申请人无审批权
	expectBiz(t, s.ApproveActivity(uidWang, id), "forbidden", "运营审批")

	// 退回必须写原因；退回后可修改再提交（复用同一申请，避免活动互斥）
	expectBiz(t, s.RejectActivity(uidLi, id, ""), "bad_reason", "退回空原因")
	mustOK(t, s.RejectActivity(uidLi, id, "间隔不合理"), "reject")
	mustOK(t, s.UpdateApplication(uidZhang, id, "排查-修改", 60, true), "resubmit")
	a, err := s.GetActivity(id)
	mustOK(t, err, "get")
	if a.Status != model.ActSubmitted || a.RejectReason != nil {
		t.Fatalf("重新提交后状态错误: %s reject=%v", a.Status, a.RejectReason)
	}

	// 批准后参数不可修改
	mustOK(t, s.ApproveActivity(uidLi, id), "approve")
	expectBiz(t, s.UpdateApplication(uidZhang, id, "改", 40, false), "bad_state", "批准后修改")
}

func TestSingleOpenActivity(t *testing.T) {
	s := newTestSvc(t)
	id1, _ := s.CreateApplication(uidZhang, "第一单", 30)
	mustOK(t, s.SubmitApplication(uidZhang, id1), "submit1")

	// 同一车队已有未结束活动时，第二个申请不能提交
	id2, _ := s.CreateApplication(uidZhao, "第二单", 30)
	expectBiz(t, s.SubmitApplication(uidZhao, id2), "conflict", "互斥")

	// 退回 id1 后 id2 可提交（id1 不再占用）
	mustOK(t, s.RejectActivity(uidLi, id1, "退回"), "reject")
	mustOK(t, s.SubmitApplication(uidZhao, id2), "submit2")
	expectBiz(t, s.SubmitApplication(uidZhang, id1), "conflict", "反向互斥")
}

// ---------- 试点与推广 ----------

func TestPilotAndExpand(t *testing.T) {
	s := newTestSvc(t)
	id := createApproved(t, s)

	// 试点数量必须恰好 2
	expectBiz(t, s.StartPilot(uidWang, id, []string{"V1"}), "bad_pilot", "1台")
	expectBiz(t, s.StartPilot(uidWang, id, []string{"V1", "V2", "V3"}), "bad_pilot", "3台")
	expectBiz(t, s.StartPilot(uidWang, id, []string{"V1", "V1"}), "bad_pilot", "重复")
	expectBiz(t, s.StartPilot(uidWang, id, []string{"V1", "X9"}), "bad_pilot", "非本车队")

	// 发起试点：V1(在线) + V2(离线)，其余车辆不得提前获得配置
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V2"}), "pilot")
	for _, vid := range []string{"V3", "V4", "V5"} {
		_, err := s.VehiclePull("vtok-" + vid)
		expectBiz(t, err, "no_config", "未试点车不得获得配置 "+vid)
	}

	// 试点未全部成功不得推广
	expectBiz(t, s.ConfirmExpand(uidWang, id), "pilot_not_ready", "未成功即推广")

	// 重复发起试点被拒（状态已不是 approved）
	expectBiz(t, s.StartPilot(uidWang, id, []string{"V3", "V4"}), "bad_state", "重复试点")

	// v1=1, v2=1（首次下发均为版本1；V2 离线不能拉取，直接按版本回执）；V2 失败 → 活动进入试点暂停
	v1 := ackVersion(t, s, "V1")
	ackAll(t, s, []string{"V1"}, v1, model.AckSuccess)
	ackAll(t, s, []string{"V2"}, 1, model.AckFailed)
	a, _ := s.GetActivity(id)
	if a.Status != model.ActPilotPaused {
		t.Fatalf("试点失败后应暂停，实际 %s", a.Status)
	}

	// V2 迟到成功（该版本已是 failed，成功不改变结论）
	rec, err := s.VehicleAck("vtok-V2", 1, model.AckSuccess)
	mustOK(t, err, "late success on failed")
	if rec.Accepted || rec.Code != "already_final" {
		t.Fatalf("failed 后成功应仅记录: %+v", rec)
	}

	// 暂停状态下两台未同时成功 → 仍不能推广
	expectBiz(t, s.ConfirmExpand(uidWang, id), "pilot_not_ready", "暂停且未全成功")
}

func TestExpandSuccessAndComplete(t *testing.T) {
	s := newTestSvc(t)
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V2"}), "pilot")

	v1 := ackVersion(t, s, "V1")
	ackAll(t, s, []string{"V1"}, v1, model.AckSuccess)
	ackAll(t, s, []string{"V2"}, 1, model.AckSuccess) // V2 离线不能拉取，按已知版本回执
	mustOK(t, s.ConfirmExpand(uidWang, id), "expand")

	// 推广后三台均获得配置且版本为 1（首次下发）
	for _, vid := range []string{"V3", "V4", "V5"} {
		p, err := s.VehiclePull("vtok-" + vid)
		mustOK(t, err, "pull "+vid)
		if p.Version != 1 || p.Target != 30 {
			t.Fatalf("%s 拉取异常: %+v", vid, p)
		}
	}

	// 未全部成功不能记完成
	expectBiz(t, s.CompleteActivity(uidLi, id), "not_ready", "未全部成功记完成")

	// 剩余三台成功
	for _, vid := range []string{"V3", "V4", "V5"} {
		v := ackVersion(t, s, vid)
		ackAll(t, s, []string{vid}, v, model.AckSuccess)
	}

	// 记完成只能是负责人
	expectBiz(t, s.CompleteActivity(uidWang, id), "forbidden", "运营记完成")
	mustOK(t, s.CompleteActivity(uidLi, id), "complete")
	a, _ := s.GetActivity(id)
	if a.Status != model.ActCompleted {
		t.Fatalf("期望 completed，实际 %s", a.Status)
	}

	// 成功后重复回执不重复记账
	rec, err := s.VehicleAck("vtok-V1", v1, model.AckSuccess)
	mustOK(t, err, "dup ack")
	if rec.Accepted || rec.Code != "duplicate" {
		t.Fatalf("重复成功应拒绝: %+v", rec)
	}

	// 生效值核对：全部车辆应为 30s
	vs, _ := s.ListVehicles()
	for _, v := range vs {
		if v.CurrentInterval != 30 {
			t.Fatalf("%s 生效值应 30，实际 %d", v.ID, v.CurrentInterval)
		}
	}
}

// ---------- 回执鉴权与版本 ----------

func TestAckAuthAndVersions(t *testing.T) {
	s := newTestSvc(t)
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V3"}), "pilot")

	// 令牌无效
	_, err := s.VehicleAck("bad-token", 1, model.AckSuccess)
	expectBiz(t, err, "bad_token", "无效令牌")

	// 离线车不能拉取（V2 未参与且离线；切 V4 离线测试）
	_, err = s.VehiclePull("vtok-V2")
	expectBiz(t, err, "offline", "离线拉取")

	// 未知版本
	rec, err := s.VehicleAck("vtok-V1", 99, model.AckSuccess)
	mustOK(t, err, "unknown")
	if rec.Accepted || rec.Code != "unknown_version" {
		t.Fatalf("未知版本应拒绝: %+v", rec)
	}

	// 非法结果（先于版本校验记录）
	rec, err = s.VehicleAck("vtok-V1", 1, "weird")
	mustOK(t, err, "invalid result")
	if rec.Accepted || rec.Code != "invalid_result" {
		t.Fatalf("非法结果应拒绝: %+v", rec)
	}

	// 正常完成试点与推广
	v1 := ackVersion(t, s, "V1")
	ackAll(t, s, []string{"V1"}, v1, model.AckSuccess)
	v3 := ackVersion(t, s, "V3")
	ackAll(t, s, []string{"V3"}, v3, model.AckSuccess)
	mustOK(t, s.ConfirmExpand(uidWang, id), "expand")
	v5 := ackVersion(t, s, "V5")
	ackAll(t, s, []string{"V5"}, v5, model.AckSuccess)
	// 其余推广车（V2 离线按已知版本回执）全部成功后才能记完成
	v4 := ackVersion(t, s, "V4")
	ackAll(t, s, []string{"V4"}, v4, model.AckSuccess)
	ackAll(t, s, []string{"V2"}, 1, model.AckSuccess)

	// 第二个活动（第一个已完成不互斥）：为制造更高版本
	mustOK(t, s.CompleteActivity(uidLi, id), "complete")
	id2, _ := s.CreateApplication(uidZhang, "第二单", 45)
	mustOK(t, s.SubmitApplication(uidZhang, id2), "submit2")
	mustOK(t, s.ApproveActivity(uidLi, id2), "approve2")
	mustOK(t, s.StartPilot(uidWang, id2, []string{"V5", "V4"}), "pilot2")
	v5new := ackVersion(t, s, "V5")
	if v5new != v5+1 {
		t.Fatalf("版本应递增: %d -> %d", v5, v5new)
	}

	// 跨车回执：版本 2 属于 V4/V5，用 V1（只有版本 1）的令牌回执 → 不属于本车，拒绝
	rec, err = s.VehicleAck("vtok-V1", v5new, model.AckSuccess)
	mustOK(t, err, "cross")
	if rec.Accepted || rec.Code != "unknown_version" {
		t.Fatalf("跨车回执应拒绝: %+v", rec)
	}

	// 旧版本回执不能覆盖新版本
	rec, err = s.VehicleAck("vtok-V5", v5, model.AckSuccess)
	mustOK(t, err, "stale")
	if rec.Accepted || rec.Code != "stale" {
		t.Fatalf("旧版本应拒绝: %+v", rec)
	}
	vs, _ := s.ListVehicles()
	var v5v *model.Vehicle
	for i := range vs {
		if vs[i].ID == "V5" {
			v5v = &vs[i]
		}
	}
	if v5v.CurrentInterval != 30 {
		t.Fatalf("旧版本生效值不应被覆盖，当前 %d", v5v.CurrentInterval)
	}
}

// ---------- 超时与迟到成功 ----------

func TestTimeoutAndLateSuccess(t *testing.T) {
	s := newTestSvc(t)
	base := time.Now().UTC().Truncate(time.Second)
	s.Now = func() time.Time { return base }
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V2"}), "pilot")

	// 60 秒内不超时
	n, err := s.MarkTimeouts()
	mustOK(t, err, "mark1")
	if n != 0 {
		t.Fatalf("不应有超时: %d", n)
	}

	// 推进 61 秒 → 超时，活动进入试点暂停
	s.Now = func() time.Time { return base.Add(61 * time.Second) }
	n, err = s.MarkTimeouts()
	mustOK(t, err, "mark2")
	if n != 2 {
		t.Fatalf("应标记 2 条超时，实际 %d", n)
	}
	a, _ := s.GetActivity(id)
	if a.Status != model.ActPilotPaused {
		t.Fatalf("试点超时应暂停，实际 %s", a.Status)
	}
	// 幂等：再次判定不多记
	n, _ = s.MarkTimeouts()
	if n != 0 {
		t.Fatalf("重复超时判定应幂等，实际 %d", n)
	}

	// 迟到成功：更新结果并保留超时历史
	v1 := ackVersion(t, s, "V1")
	ackAll(t, s, []string{"V1"}, v1, model.AckSuccess)
	d, err := s.GetActivityDetail(id)
	mustOK(t, err, "detail")
	var found bool
	for _, p := range d.Publishes {
		if p.VehicleID == "V1" {
			found = true
			if p.Status != model.PubSuccess || !p.Late || p.TimeoutAt == nil {
				t.Fatalf("迟到成功应保留超时历史: %+v", p)
			}
		}
		if p.VehicleID == "V2" && p.Status != model.PubTimeout {
			t.Fatalf("V2 应保持超时: %+v", p)
		}
	}
	if !found {
		t.Fatal("缺少 V1 下发记录")
	}
	// 生效值已更新
	vs, _ := s.ListVehicles()
	for _, v := range vs {
		if v.ID == "V1" && v.CurrentInterval != 30 {
			t.Fatalf("V1 生效值应 30，实际 %d", v.CurrentInterval)
		}
	}
	// 活动不因迟到回执自动重启
	a, _ = s.GetActivity(id)
	if a.Status != model.ActPilotPaused {
		t.Fatalf("迟到回执不应自动重启活动，实际 %s", a.Status)
	}
}

func TestFailedReceiptKeepsEffective(t *testing.T) {
	s := newTestSvc(t)
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V3"}), "pilot")
	v3 := ackVersion(t, s, "V3")
	ackAll(t, s, []string{"V3"}, v3, model.AckFailed)
	vs, _ := s.ListVehicles()
	for _, v := range vs {
		if v.ID == "V3" && v.CurrentInterval != 60 {
			t.Fatalf("失败不应改变生效值，实际 %d", v.CurrentInterval)
		}
	}
}

// ---------- 停止与恢复 ----------

func TestStopAndRestore(t *testing.T) {
	s := newTestSvc(t)
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V2"}), "pilot")
	v1 := ackVersion(t, s, "V1")
	ackAll(t, s, []string{"V1"}, v1, model.AckSuccess)

	// 停止必须写原因
	expectBiz(t, s.StopActivity(uidWang, id, ""), "bad_reason", "停止空原因")
	mustOK(t, s.StopActivity(uidWang, id, "排查方向变更"), "stop")

	// 停止不抹掉已生效记录
	vs, _ := s.ListVehicles()
	for _, v := range vs {
		if v.ID == "V1" && v.CurrentInterval != 30 {
			t.Fatalf("停止后生效值应保留，实际 %d", v.CurrentInterval)
		}
	}

	// 已停止活动不能继续下发/推广/记完成
	expectBiz(t, s.ConfirmExpand(uidWang, id), "bad_state", "停止后推广")

	// 迟到回执不重启活动（V2 离线不能拉取，按已知版本 1 回执）
	ackAll(t, s, []string{"V2"}, 1, model.AckSuccess)
	a, _ := s.GetActivity(id)
	if a.Status != model.ActStopped {
		t.Fatalf("迟到回执不应重启已停止活动，实际 %s", a.Status)
	}

	// 恢复：发起 → 批准 → 执行 → 全部成功 → 记恢复完成
	mustOK(t, s.RequestRestore(uidWang, id), "request restore")
	expectBiz(t, s.RequestRestore(uidWang, id), "bad_state", "重复发起恢复")
	expectBiz(t, s.ApproveRestore(uidWang, id), "forbidden", "运营批恢复")
	mustOK(t, s.ApproveRestore(uidLi, id), "approve restore")
	mustOK(t, s.ExecuteRestore(uidWang, id), "execute restore")

	// 恢复版本更高（V1: v1+1），目标为活动前值 60
	p, err := s.VehiclePull("vtok-V1")
	mustOK(t, err, "pull restore")
	if p.Version != v1+1 || p.Target != 60 || p.Batch != model.BatchRestore {
		t.Fatalf("恢复下发异常: %+v", p)
	}
	// V2 也曾获得下发 → 也有恢复版本（先切在线才能拉取）
	mustOK(t, s.SetVehicleOnline("V2", true), "v2 online")
	p2, err := s.VehiclePull("vtok-V2")
	mustOK(t, err, "pull restore v2")
	if p2.Batch != model.BatchRestore || p2.Target != 60 {
		t.Fatalf("V2 恢复下发异常: %+v", p2)
	}

	// 未全部确认不得记恢复完成
	expectBiz(t, s.MarkRestored(uidLi, id), "restore_not_ready", "未全部确认")
	ackAll(t, s, []string{"V1"}, p.Version, model.AckSuccess)
	expectBiz(t, s.MarkRestored(uidLi, id), "restore_not_ready", "仍有未确认项")
	ackAll(t, s, []string{"V2"}, p2.Version, model.AckSuccess)
	mustOK(t, s.MarkRestored(uidLi, id), "mark restored")
	a, _ = s.GetActivity(id)
	if a.Status != model.ActRestored {
		t.Fatalf("期望 restored，实际 %s", a.Status)
	}
	// V2 已生效值恢复为 60
	vs, _ = s.ListVehicles()
	for _, v := range vs {
		if v.ID == "V2" && v.CurrentInterval != 60 {
			t.Fatalf("V2 恢复后生效值应 60，实际 %d", v.CurrentInterval)
		}
	}
}

func TestRestoreWithoutPublishRejected(t *testing.T) {
	s := newTestSvc(t)
	// approved / submitted / rejected / draft 状态均不能停止
	id := createApproved(t, s)
	expectBiz(t, s.StopActivity(uidWang, id, "x"), "bad_state", "approved 停止")
	id2, _ := s.CreateApplication(uidZhang, "x", 30)
	expectBiz(t, s.StopActivity(uidWang, id2, "x"), "bad_state", "draft 停止")
}

// ---------- 并发 ----------

func TestConcurrentAckDuplicate(t *testing.T) {
	s := newTestSvc(t)
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V3"}), "pilot")
	v1 := ackVersion(t, s, "V1")

	// 并发重复成功回执：最终只能生效一次
	errCh := make(chan error, 2)
	recCh := make(chan *model.Receipt, 2)
	for i := 0; i < 2; i++ {
		go func() {
			rec, err := s.VehicleAck("vtok-V1", v1, model.AckSuccess)
			errCh <- err
			recCh <- rec
		}()
	}
	acceptedCount := 0
	for i := 0; i < 2; i++ {
		mustOK(t, <-errCh, "concurrent ack")
		if (<-recCh).Accepted {
			acceptedCount++
		}
	}
	if acceptedCount != 1 {
		t.Fatalf("并发回执应恰好受理一次，实际 %d", acceptedCount)
	}
	vs, _ := s.ListVehicles()
	for _, v := range vs {
		if v.ID == "V1" && v.CurrentInterval != 30 {
			t.Fatalf("并发后生效值错误: %d", v.CurrentInterval)
		}
	}
}

// ---------- 查询与辅助 ----------

func TestQueries(t *testing.T) {
	s := newTestSvc(t)
	id := createApproved(t, s)

	// 用户与车辆
	u, err := s.GetUser(uidWang)
	mustOK(t, err, "get user")
	if u.Role != model.RoleOperator {
		t.Fatalf("角色错误: %s", u.Role)
	}
	_, err = s.GetUser(999)
	expectBiz(t, err, "not_found", "用户不存在")
	users, err := s.ListUsers()
	mustOK(t, err, "list users")
	if len(users) != 4 {
		t.Fatalf("预置 4 个账号，实际 %d", len(users))
	}
	vs, err := s.ListVehicles()
	mustOK(t, err, "list vehicles")
	if len(vs) != 5 {
		t.Fatalf("预置 5 台车，实际 %d", len(vs))
	}

	// 本人视角只看自己的申请
	mine, err := s.ListActivities(uidZhang)
	mustOK(t, err, "list mine")
	if len(mine) != 1 {
		t.Fatalf("张研发应只见 1 个申请，实际 %d", len(mine))
	}
	all, err := s.ListActivities(0)
	mustOK(t, err, "list all")
	if len(all) < 1 {
		t.Fatal("全部列表不应为空")
	}

	// 车辆在线切换
	mustOK(t, s.SetVehicleOnline("V2", true), "online")
	vs, _ = s.ListVehicles()
	for _, v := range vs {
		if v.ID == "V2" && !v.Online {
			t.Fatal("V2 应已在线")
		}
	}

	// 详情与回执列表
	d, err := s.GetActivityDetail(id)
	mustOK(t, err, "detail")
	if d.Activity.ID != id || len(d.Vehicles) != 5 {
		t.Fatalf("详情数据异常: %+v", d.Activity)
	}
	_, err = s.GetActivityDetail(999)
	expectBiz(t, err, "not_found", "活动不存在")

	rec, err := s.VehicleAck("vtok-V1", 99, model.AckSuccess)
	mustOK(t, err, "rec")
	_ = rec
	rs, err := s.ListReceipts()
	mustOK(t, err, "list receipts")
	if len(rs) == 0 {
		t.Fatal("回执列表不应为空")
	}
}

func TestVehiclePullLatestVersion(t *testing.T) {
	s := newTestSvc(t)
	id := createApproved(t, s)
	mustOK(t, s.StartPilot(uidWang, id, []string{"V1", "V4"}), "pilot")
	p, err := s.VehiclePull("vtok-V4")
	mustOK(t, err, "pull")
	if p.Version != 1 || p.Target != 30 || p.Batch != model.BatchPilot {
		t.Fatalf("拉取最新版本异常: %+v", p)
	}
	_ = fmt.Sprint(id)
}
