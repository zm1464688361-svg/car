// Package service 实现题目09核心业务：申请、审批、试点、推广、停止、恢复、回执与超时。
// 所有状态迁移在事务中完成；身份与权限由调用方（web 层）传入 user，业务规则在本层校验。
package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zm1464688361-svg/car/internal/model"
)

// Service 业务服务。Now 可注入以便测试超时；Timeout 为回执超时教学时钟。
type Service struct {
	DB      *sql.DB
	Timeout time.Duration
	Now     func() time.Time
}

// New 创建服务，默认超时 60 秒（题面规定），可用 SetTimeout 调整教学时钟。
func New(db *sql.DB) *Service {
	return &Service{DB: db, Timeout: 60 * time.Second, Now: time.Now}
}

func (s *Service) now() time.Time { return s.Now().UTC().Truncate(time.Second) }

func tx(db *sql.DB, fn func(*sql.Tx) error) error {
	t, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(t); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}

func audit(t *sql.Tx, at time.Time, activityID int64, actor, action, detail string) error {
	_, err := t.Exec(`INSERT INTO audits(activity_id, actor, action, detail, at) VALUES(?,?,?,?,?)`,
		activityID, actor, action, detail, at.Format(time.RFC3339))
	return err
}

// ---------- 用户与车辆 ----------

// GetUser 按 ID 取用户。
func (s *Service) GetUser(id int64) (*model.User, error) {
	var u model.User
	err := s.DB.QueryRow(`SELECT id, name, role FROM users WHERE id=?`, id).Scan(&u.ID, &u.Name, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.Err("not_found", "用户不存在")
	}
	return &u, err
}

// ListUsers 列出全部预置账号。
func (s *Service) ListUsers() ([]model.User, error) {
	rows, err := s.DB.Query(`SELECT id, name, role FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Name, &u.Role); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListVehicles 列出车队（不含令牌）。
func (s *Service) ListVehicles() ([]model.Vehicle, error) {
	rows, err := s.DB.Query(`SELECT id, name, online, current_version, current_interval, baseline FROM vehicles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Vehicle
	for rows.Next() {
		var v model.Vehicle
		var online int
		if err := rows.Scan(&v.ID, &v.Name, &online, &v.CurrentVersion, &v.CurrentInterval, &v.Baseline); err != nil {
			return nil, err
		}
		v.Online = online == 1
		out = append(out, v)
	}
	return out, rows.Err()
}

func loadVehicle(t *sql.Tx, id string) (*model.Vehicle, error) {
	var v model.Vehicle
	var online int
	err := t.QueryRow(`SELECT id, name, online, current_version, current_interval, baseline FROM vehicles WHERE id=?`, id).
		Scan(&v.ID, &v.Name, &online, &v.CurrentVersion, &v.CurrentInterval, &v.Baseline)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.Err("not_found", "车辆不存在")
	}
	v.Online = online == 1
	return &v, err
}

func loadVehicleByToken(t *sql.Tx, token string) (*model.Vehicle, error) {
	var v model.Vehicle
	var online int
	err := t.QueryRow(`SELECT id, name, online, current_version, current_interval, baseline FROM vehicles WHERE token=?`, token).
		Scan(&v.ID, &v.Name, &online, &v.CurrentVersion, &v.CurrentInterval, &v.Baseline)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.Err("bad_token", "车辆令牌无效")
	}
	v.Online = online == 1
	return &v, err
}

func saveVehicleOnline(t *sql.Tx, id string, online bool) error {
	o := 0
	if online {
		o = 1
	}
	_, err := t.Exec(`UPDATE vehicles SET online=? WHERE id=?`, o, id)
	return err
}

// SetVehicleOnline 模拟器切换车辆在线状态。
func (s *Service) SetVehicleOnline(vehicleID string, online bool) error {
	return tx(s.DB, func(t *sql.Tx) error {
		if _, err := loadVehicle(t, vehicleID); err != nil {
			return err
		}
		return saveVehicleOnline(t, vehicleID, online)
	})
}

// ---------- 申请（研发申请人） ----------

func validateInterval(sec int) error {
	if sec < model.IntervalMin || sec > model.IntervalMax {
		return model.Err("bad_interval", fmt.Sprintf("日志间隔须在 %d~%d 秒之间", model.IntervalMin, model.IntervalMax))
	}
	return nil
}

// CreateApplication 创建申请草稿。申请默认覆盖全部五台车（车队固定，见假设A1）。
func (s *Service) CreateApplication(userID int64, reason string, interval int) (int64, error) {
	if reason == "" {
		return 0, model.Err("bad_reason", "必须填写排查原因")
	}
	if err := validateInterval(interval); err != nil {
		return 0, err
	}
	var id int64
	err := tx(s.DB, func(t *sql.Tx) error {
		now := s.now()
		res, err := t.Exec(`INSERT INTO activities(applicant_id, reason, interval, status, created_at, updated_at) VALUES(?,?,?,?,?,?)`,
			userID, reason, interval, model.ActDraft, now.Format(time.RFC3339), now.Format(time.RFC3339))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		return audit(t, now, id, fmt.Sprintf("user:%d", userID), "create_application", fmt.Sprintf("原因=%s 间隔=%ds", reason, interval))
	})
	return id, err
}

// UpdateApplication 退回/草稿状态下修改并可直接重新提交（题面：退回后可修改再提交）。
func (s *Service) UpdateApplication(userID int64, activityID int64, reason string, interval int, resubmit bool) error {
	if reason == "" {
		return model.Err("bad_reason", "必须填写排查原因")
	}
	if err := validateInterval(interval); err != nil {
		return err
	}
	return tx(s.DB, func(t *sql.Tx) error {
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.ApplicantID != userID {
			return model.Err("forbidden", "只能修改本人的申请")
		}
		if a.Status != model.ActDraft && a.Status != model.ActRejected {
			return model.Err("bad_state", "批准后的参数不可原地修改，当前状态："+model.ActLabel(a.Status))
		}
		now := s.now()
		newStatus := model.ActDraft
		if resubmit {
			newStatus = model.ActSubmitted
		}
		if _, err := t.Exec(`UPDATE activities SET reason=?, interval=?, status=?, reject_reason=NULL, updated_at=? WHERE id=?`,
			reason, interval, newStatus, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		action := "update_application"
		if resubmit {
			action = "resubmit_application"
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", userID), action, fmt.Sprintf("原因=%s 间隔=%ds", reason, interval))
	})
}

// SubmitApplication 提交草稿进入待审批；同一车队存在未结束活动时拒绝（R2）。
func (s *Service) SubmitApplication(userID int64, activityID int64) error {
	return tx(s.DB, func(t *sql.Tx) error {
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.ApplicantID != userID {
			return model.Err("forbidden", "只能提交本人的申请")
		}
		if a.Status != model.ActDraft && a.Status != model.ActRejected {
			return model.Err("bad_state", "当前状态不可提交："+model.ActLabel(a.Status))
		}
		open, err := hasOpenActivity(t, activityID)
		if err != nil {
			return err
		}
		if open {
			return model.Err("conflict", "同一车队同时只允许一个未结束发布活动")
		}
		now := s.now()
		if _, err := t.Exec(`UPDATE activities SET status=?, updated_at=? WHERE id=?`,
			model.ActSubmitted, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", userID), "submit_application", "")
	})
}

// openStatuses 视为“未结束”的活动状态（同一车队互斥）。
var openStatuses = []string{
	model.ActSubmitted, model.ActApproved, model.ActPiloting, model.ActPilotPaused,
	model.ActExpanding, model.ActRestoreRequested, model.ActRestoreApproved, model.ActRestoring,
}

func hasOpenActivity(t *sql.Tx, excludeID int64) (bool, error) {
	var n int
	err := t.QueryRow(fmt.Sprintf(
		`SELECT COUNT(*) FROM activities WHERE id<>%d AND status IN ('%s','%s','%s','%s','%s','%s','%s','%s')`,
		excludeID, openStatuses[0], openStatuses[1], openStatuses[2], openStatuses[3],
		openStatuses[4], openStatuses[5], openStatuses[6], openStatuses[7])).Scan(&n)
	return n > 0, err
}

// ---------- 审批（负责人，不能自审） ----------

// requireRoleTx 服务端角色校验（防越权：身份由会话决定，不信任请求参数）。
func requireRoleTx(t *sql.Tx, userID int64, roles ...model.Role) error {
	var r string
	err := t.QueryRow(`SELECT role FROM users WHERE id=?`, userID).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Err("forbidden", "操作者身份不存在")
	}
	if err != nil {
		return err
	}
	for _, want := range roles {
		if model.Role(r) == want {
			return nil
		}
	}
	return model.Err("forbidden", "当前身份无权执行该操作")
}

// ApproveActivity 批准申请。
func (s *Service) ApproveActivity(approverID, activityID int64) error {
	return tx(s.DB, func(t *sql.Tx) error {
		if err := requireRoleTx(t, approverID, model.RoleApprover); err != nil {
			return err
		}
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.Status != model.ActSubmitted {
			return model.Err("bad_state", "仅待审批状态可批准，当前："+model.ActLabel(a.Status))
		}
		if a.ApplicantID == approverID {
			return model.Err("forbidden", "审批人不能是申请人")
		}
		now := s.now()
		if _, err := t.Exec(`UPDATE activities SET status=?, approver_id=?, updated_at=? WHERE id=?`,
			model.ActApproved, approverID, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", approverID), "approve", "")
	})
}

// RejectActivity 退回申请，须写原因。
func (s *Service) RejectActivity(approverID, activityID int64, reason string) error {
	if reason == "" {
		return model.Err("bad_reason", "退回必须写明原因")
	}
	return tx(s.DB, func(t *sql.Tx) error {
		if err := requireRoleTx(t, approverID, model.RoleApprover); err != nil {
			return err
		}
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.Status != model.ActSubmitted {
			return model.Err("bad_state", "仅待审批状态可退回，当前："+model.ActLabel(a.Status))
		}
		if a.ApplicantID == approverID {
			return model.Err("forbidden", "审批人不能是申请人")
		}
		now := s.now()
		if _, err := t.Exec(`UPDATE activities SET status=?, approver_id=?, reject_reason=?, updated_at=? WHERE id=?`,
			model.ActRejected, approverID, reason, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", approverID), "reject", reason)
	})
}

// ---------- 试点与推广（运营） ----------

// StartPilot 发起试点：恰好选择 2 台车下发，其余车辆不得提前获得配置（R2/R3）。
func (s *Service) StartPilot(operatorID, activityID int64, pilotIDs []string) error {
	if len(pilotIDs) != model.PilotCount {
		return model.Err("bad_pilot", "试点必须恰好选择 2 台车")
	}
	seen := map[string]bool{}
	for _, id := range pilotIDs {
		if seen[id] {
			return model.Err("bad_pilot", "试点车辆不能重复")
		}
		seen[id] = true
	}
	return tx(s.DB, func(t *sql.Tx) error {
		if err := requireRoleTx(t, operatorID, model.RoleOperator); err != nil {
			return err
		}
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.Status != model.ActApproved {
			return model.Err("bad_state", "仅待试点状态可发起试点，当前："+model.ActLabel(a.Status))
		}
		all, err := fleetIDs(t)
		if err != nil {
			return err
		}
		for _, id := range pilotIDs {
			if !all[id] {
				return model.Err("bad_pilot", "试点车辆必须是本车队车辆："+id)
			}
		}
		now := s.now()
		pj, _ := json.Marshal(pilotIDs)
		if _, err := t.Exec(`UPDATE activities SET status=?, pilot_ids=?, updated_at=? WHERE id=?`,
			model.ActPiloting, string(pj), now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		for _, vid := range pilotIDs {
			if err := publish(t, s.now(), activityID, vid, a.Interval, model.BatchPilot); err != nil {
				return err
			}
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", operatorID), "start_pilot", fmt.Sprintf("试点车=%v", pilotIDs))
	})
}

func fleetIDs(t *sql.Tx) (map[string]bool, error) {
	rows, err := t.Query(`SELECT id FROM vehicles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		m[id] = true
	}
	return m, rows.Err()
}

// publish 为车辆创建更高版本的下发记录（每车版本递增；首次下发记录活动前基线）。
func publish(t *sql.Tx, now time.Time, activityID int64, vehicleID string, target int, batch string) error {
	v, err := loadVehicle(t, vehicleID)
	if err != nil {
		return err
	}
	if v.Baseline == nil {
		b := v.CurrentInterval
		if _, err := t.Exec(`UPDATE vehicles SET baseline=? WHERE id=?`, b, vehicleID); err != nil {
			return err
		}
	}
	version := v.CurrentVersion + 1
	_, err = t.Exec(`INSERT INTO publishes(activity_id, vehicle_id, version, target, batch, status, published_at)
		VALUES(?,?,?,?,?,?,?)`,
		activityID, vehicleID, version, target, batch, model.PubPublished, now.Format(time.RFC3339))
	if err != nil {
		return err
	}
	_, err = t.Exec(`UPDATE vehicles SET current_version=? WHERE id=?`, version, vehicleID)
	return err
}

// ConfirmExpand 试点两台全部成功后，推广至剩余三台（R3）。失败/超时未处理不得绕过。
// 试点曾超时进入暂停后，若迟到成功使两台均成功，同样允许推广。
func (s *Service) ConfirmExpand(operatorID, activityID int64) error {
	return tx(s.DB, func(t *sql.Tx) error {
		if err := requireRoleTx(t, operatorID, model.RoleOperator); err != nil {
			return err
		}
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.Status != model.ActPiloting && a.Status != model.ActPilotPaused {
			return model.Err("bad_state", "仅试点中/试点暂停状态可确认推广，当前："+model.ActLabel(a.Status))
		}
		pubs, err := activityPublishes(t, activityID)
		if err != nil {
			return err
		}
		var pilot []model.Publish
		for _, p := range pubs {
			if p.Batch == model.BatchPilot {
				pilot = append(pilot, p)
			}
		}
		if len(pilot) != model.PilotCount {
			return model.Err("bad_pilot", "试点下载数量异常")
		}
		for _, p := range pilot {
			if p.Status != model.PubSuccess {
				return model.Err("pilot_not_ready",
					fmt.Sprintf("试点车 %s 状态为 %s，两台试点均成功后才可推广", p.VehicleID, model.PubLabel(p.Status)))
			}
		}
		fleet, err := fleetIDs(t)
		if err != nil {
			return err
		}
		now := s.now()
		if _, err := t.Exec(`UPDATE activities SET status=?, updated_at=? WHERE id=?`,
			model.ActExpanding, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		var rest []string
		for _, p := range pilot {
			delete(fleet, p.VehicleID)
		}
		for id := range fleet {
			rest = append(rest, id)
		}
		for _, vid := range rest {
			if err := publish(t, s.now(), activityID, vid, a.Interval, model.BatchExpand); err != nil {
				return err
			}
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", operatorID), "confirm_expand", fmt.Sprintf("推广车=%v", rest))
	})
}

// ---------- 停止与恢复 ----------

// StopActivity 停止活动：仅阻止未下发部分继续下发，不撤销已生效配置（R7）。
func (s *Service) StopActivity(actorID, activityID int64, reason string) error {
	if reason == "" {
		return model.Err("bad_reason", "停止活动必须写明原因")
	}
	return tx(s.DB, func(t *sql.Tx) error {
		if err := requireRoleTx(t, actorID, model.RoleOperator, model.RoleApprover); err != nil {
			return err
		}
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		switch a.Status {
		case model.ActPiloting, model.ActPilotPaused, model.ActExpanding:
		default:
			return model.Err("bad_state", "当前状态不可停止："+model.ActLabel(a.Status))
		}
		now := s.now()
		if _, err := t.Exec(`UPDATE activities SET status=?, stop_reason=?, updated_at=? WHERE id=?`,
			model.ActStopped, reason, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", actorID), "stop", reason)
	})
}

// RequestRestore 运营发起恢复（活动已停止且曾有下发记录）。
func (s *Service) RequestRestore(operatorID, activityID int64) error {
	return tx(s.DB, func(t *sql.Tx) error {
		if err := requireRoleTx(t, operatorID, model.RoleOperator); err != nil {
			return err
		}
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.Status != model.ActStopped {
			return model.Err("bad_state", "仅已停止的活动可发起恢复，当前："+model.ActLabel(a.Status))
		}
		var n int
		if err := t.QueryRow(`SELECT COUNT(*) FROM publishes WHERE activity_id=?`, activityID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return model.Err("nothing_to_restore", "该活动没有已发布配置，无需恢复")
		}
		now := s.now()
		if _, err := t.Exec(`UPDATE activities SET status=?, updated_at=? WHERE id=?`,
			model.ActRestoreRequested, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", operatorID), "request_restore", "")
	})
}

// ApproveRestore 负责人批准恢复。
func (s *Service) ApproveRestore(approverID, activityID int64) error {
	return tx(s.DB, func(t *sql.Tx) error {
		if err := requireRoleTx(t, approverID, model.RoleApprover); err != nil {
			return err
		}
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.Status != model.ActRestoreRequested {
			return model.Err("bad_state", "仅恢复待批状态可批准，当前："+model.ActLabel(a.Status))
		}
		if a.ApplicantID == approverID {
			return model.Err("forbidden", "审批人不能是申请人")
		}
		now := s.now()
		if _, err := t.Exec(`UPDATE activities SET status=?, updated_at=? WHERE id=?`,
			model.ActRestoreApproved, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", approverID), "approve_restore", "")
	})
}

// ExecuteRestore 运营执行恢复：为曾发布过配置的车辆按活动前值创建新的恢复版本（R7）。
func (s *Service) ExecuteRestore(operatorID, activityID int64) error {
	return tx(s.DB, func(t *sql.Tx) error {
		if err := requireRoleTx(t, operatorID, model.RoleOperator); err != nil {
			return err
		}
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.Status != model.ActRestoreApproved {
			return model.Err("bad_state", "仅恢复已批状态可执行恢复，当前："+model.ActLabel(a.Status))
		}
		rows, err := t.Query(`SELECT DISTINCT vehicle_id FROM publishes WHERE activity_id=? ORDER BY vehicle_id`, activityID)
		if err != nil {
			return err
		}
		var vids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			vids = append(vids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		now := s.now()
		if _, err := t.Exec(`UPDATE activities SET status=?, updated_at=? WHERE id=?`,
			model.ActRestoring, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		for _, vid := range vids {
			v, err := loadVehicle(t, vid)
			if err != nil {
				return err
			}
			if v.Baseline == nil {
				return model.Err("no_baseline", "车辆缺少活动前基线值："+vid)
			}
			if err := publish(t, s.now(), activityID, vid, *v.Baseline, model.BatchRestore); err != nil {
				return err
			}
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", operatorID), "execute_restore", fmt.Sprintf("恢复车=%v", vids))
	})
}

// MarkRestored 全部恢复下发确认成功后，负责人将活动记为恢复完成（R8）。
func (s *Service) MarkRestored(approverID, activityID int64) error {
	return tx(s.DB, func(t *sql.Tx) error {
		if err := requireRoleTx(t, approverID, model.RoleApprover); err != nil {
			return err
		}
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.Status != model.ActRestoring {
			return model.Err("bad_state", "仅恢复中状态可记恢复完成，当前："+model.ActLabel(a.Status))
		}
		pubs, err := activityPublishes(t, activityID)
		if err != nil {
			return err
		}
		for _, p := range pubs {
			if p.Batch == model.BatchRestore && p.Status != model.PubSuccess {
				return model.Err("restore_not_ready",
					fmt.Sprintf("车辆 %s 尚未确认恢复（%s），仍有未确认项不得显示全部恢复", p.VehicleID, model.PubLabel(p.Status)))
			}
		}
		now := s.now()
		if _, err := t.Exec(`UPDATE activities SET status=?, updated_at=? WHERE id=?`,
			model.ActRestored, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", approverID), "mark_restored", "")
	})
}

// CompleteActivity 全部五台目标车辆确认成功后，负责人可将活动记为完成（R8）。
func (s *Service) CompleteActivity(approverID, activityID int64) error {
	return tx(s.DB, func(t *sql.Tx) error {
		if err := requireRoleTx(t, approverID, model.RoleApprover); err != nil {
			return err
		}
		a, err := loadActivity(t, activityID)
		if err != nil {
			return err
		}
		if a.Status != model.ActExpanding {
			return model.Err("bad_state", "仅推广中状态可记完成，当前："+model.ActLabel(a.Status))
		}
		pubs, err := activityPublishes(t, activityID)
		if err != nil {
			return err
		}
		fleet, err := fleetIDs(t)
		if err != nil {
			return err
		}
		okByVehicle := map[string]bool{}
		for _, p := range pubs {
			if p.Batch == model.BatchPilot || p.Batch == model.BatchExpand {
				if p.Status != model.PubSuccess {
					return model.Err("not_ready",
						fmt.Sprintf("车辆 %s 尚未确认成功（%s），不能记完成", p.VehicleID, model.PubLabel(p.Status)))
				}
				okByVehicle[p.VehicleID] = true
			}
		}
		for id := range fleet {
			if !okByVehicle[id] {
				return model.Err("not_ready", "车辆 "+id+" 未获得本活动配置，不能记完成")
			}
		}
		now := s.now()
		if _, err := t.Exec(`UPDATE activities SET status=?, updated_at=? WHERE id=?`,
			model.ActCompleted, now.Format(time.RFC3339), activityID); err != nil {
			return err
		}
		return audit(t, now, activityID, fmt.Sprintf("user:%d", approverID), "complete", "")
	})
}

// ---------- 车端：拉取配置与回执 ----------

// VehiclePull 车辆凭令牌拉取面向自己的最新已发布版本（R4）。离线车拉取失败（假设A7）。
func (s *Service) VehiclePull(token string) (*model.Publish, error) {
	var out *model.Publish
	err := tx(s.DB, func(t *sql.Tx) error {
		v, err := loadVehicleByToken(t, token)
		if err != nil {
			return err
		}
		if !v.Online {
			return model.Err("offline", "车辆离线，无法拉取配置")
		}
		p, err := latestPublish(t, v.ID)
		if err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}

func latestPublish(t *sql.Tx, vehicleID string) (*model.Publish, error) {
	var p model.Publish
	var ackAt, failReason, timeoutAt sql.NullString
	var late int
	err := t.QueryRow(`SELECT id, activity_id, vehicle_id, version, target, batch, status, published_at, ack_at, fail_reason, timeout_at, late
		FROM publishes WHERE vehicle_id=? ORDER BY version DESC LIMIT 1`, vehicleID).
		Scan(&p.ID, &p.ActivityID, &p.VehicleID, &p.Version, &p.Target, &p.Batch, &p.Status, &p.PublishedAt, &ackAt, &failReason, &timeoutAt, &late)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.Err("no_config", "车辆没有已发布配置")
	}
	if err != nil {
		return nil, err
	}
	p.Late = late == 1
	scanOptTime(ackAt, &p.AckAt)
	scanOptTime(timeoutAt, &p.TimeoutAt)
	if failReason.Valid {
		p.FailReason = &failReason.String
	}
	return &p, nil
}

// VehicleAck 车端回执：鉴权+版本匹配+去重+旧版本拒绝（R5）。
// 返回回执处理记录（accepted=false 表示拒绝并已记录诊断）。
func (s *Service) VehicleAck(token string, version int, result string) (*model.Receipt, error) {
	var rec *model.Receipt
	err := tx(s.DB, func(t *sql.Tx) error {
		now := s.now()
		v, err := loadVehicleByToken(t, token)
		if err != nil {
			return err // 令牌无效：无法归属车辆，不记录
		}
		rec = &model.Receipt{VehicleID: v.ID, Version: version, Result: result, At: now.Format(time.RFC3339)}
		if result != model.AckSuccess && result != model.AckFailed {
			rec.Code, rec.Note = "invalid_result", "回执结果只能是 success/failed"
			return saveReceipt(t, rec)
		}
		var pubID int64
		var pubStatus, pubVehicle string
		err = t.QueryRow(`SELECT id, vehicle_id, status FROM publishes WHERE version=? AND vehicle_id=?`, version, v.ID).
			Scan(&pubID, &pubVehicle, &pubStatus)
		if errors.Is(err, sql.ErrNoRows) {
			// 未知版本：含跨车回执（版本属于他车）与凭空版本
			rec.Code, rec.Note = "unknown_version", "回执版本不存在或不属于本车，拒绝并记录"
			return saveReceipt(t, rec)
		}
		if err != nil {
			return err
		}
		if version < v.CurrentVersion {
			rec.Code, rec.Note = "stale", "旧版本回执不能覆盖新版本"
			return saveReceipt(t, rec)
		}
		if pubStatus == model.PubSuccess {
			rec.Code, rec.Note = "duplicate", "重复成功回执不重复记账"
			return saveReceipt(t, rec)
		}
		switch result {
		case model.AckSuccess:
			if pubStatus == model.PubPublished || pubStatus == model.PubTimeout {
				late := pubStatus == model.PubTimeout
				l := 0
				if late {
					l = 1
				}
				if _, err := t.Exec(`UPDATE publishes SET status=?, ack_at=?, late=? WHERE id=?`,
					model.PubSuccess, now.Format(time.RFC3339), l, pubID); err != nil {
					return err
				}
				// 成功回执更新该车已生效值（期望值落地）
				if _, err := t.Exec(`UPDATE vehicles SET current_interval=(SELECT target FROM publishes WHERE id=?) WHERE id=?`,
					pubID, v.ID); err != nil {
					return err
				}
				rec.Accepted, rec.Code, rec.Note = true, "ok", "回执受理，车端已生效"
				if late {
					rec.Note = "超时后迟到成功，更新该车结果并保留超时历史"
				}
			} else {
				rec.Code, rec.Note = "already_final", "该版本已有失败结论，成功不改变结果"
			}
		case model.AckFailed:
			if pubStatus == model.PubPublished {
				if _, err := t.Exec(`UPDATE publishes SET status=?, ack_at=?, fail_reason=? WHERE id=?`,
					model.PubFailed, now.Format(time.RFC3339), "车辆上报应用失败", pubID); err != nil {
					return err
				}
				rec.Accepted, rec.Code, rec.Note = true, "ok", "失败回执受理，不改变此前生效值"
			} else {
				rec.Code, rec.Note = "already_final", "该版本已超时，失败仅记录不改变结论"
			}
		}
		if err := saveReceipt(t, rec); err != nil {
			return err
		}
		// 试点失败回执 → 活动进入试点暂停（R3）
		if result == model.AckFailed && rec.Accepted && pubStatus == model.PubPublished {
			var batch string
			var actID int64
			if err := t.QueryRow(`SELECT batch, activity_id FROM publishes WHERE id=?`, pubID).Scan(&batch, &actID); err != nil {
				return err
			}
			if batch == model.BatchPilot {
				var st string
				if err := t.QueryRow(`SELECT status FROM activities WHERE id=?`, actID).Scan(&st); err != nil {
					return err
				}
				if st == model.ActPiloting {
					if _, err := t.Exec(`UPDATE activities SET status=?, updated_at=? WHERE id=?`,
						model.ActPilotPaused, now.Format(time.RFC3339), actID); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rec, nil
}

func saveReceipt(t *sql.Tx, r *model.Receipt) error {
	acc := 0
	if r.Accepted {
		acc = 1
	}
	res, err := t.Exec(`INSERT INTO receipts(vehicle_id, version, result, accepted, code, note, at) VALUES(?,?,?,?,?,?,?)`,
		r.VehicleID, r.Version, r.Result, acc, r.Code, r.Note, r.At)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}
// MarkTimeouts 超时判定：发布超过 Timeout 无有效回执 → timeout（R6）。
// 试点超时同时把活动置为 pilot_paused。幂等：已是终态的不处理。
func (s *Service) MarkTimeouts() (int, error) {
	marked := 0
	err := tx(s.DB, func(t *sql.Tx) error {
		now := s.now()
		// 截断到秒，与 published_at 的存储格式一致，保证字符串比较正确
		cutoff := now.Add(-s.Timeout).Truncate(time.Second).Format(time.RFC3339)
		rows, err := t.Query(`SELECT p.id, p.activity_id, p.batch, a.status
			FROM publishes p JOIN activities a ON a.id=p.activity_id
			WHERE p.status=? AND p.published_at <= ?`, model.PubPublished, cutoff)
		if err != nil {
			return err
		}
		type item struct {
			id, actID   int64
			batch, aSt  string
		}
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.actID, &it.batch, &it.aSt); err != nil {
				rows.Close()
				return err
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, it := range items {
			if _, err := t.Exec(`UPDATE publishes SET status=?, timeout_at=? WHERE id=? AND status=?`,
				model.PubTimeout, now.Format(time.RFC3339), it.id, model.PubPublished); err != nil {
				return err
			}
			marked++
			if it.batch == model.BatchPilot && it.aSt == model.ActPiloting {
				if _, err := t.Exec(`UPDATE activities SET status=?, updated_at=? WHERE id=?`,
					model.ActPilotPaused, now.Format(time.RFC3339), it.actID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return marked, err
}

// StartTimeoutTicker 启动后台教学时钟，按 tick 周期判定超时。
func (s *Service) StartTimeoutTicker(tick time.Duration) chan struct{} {
	stop := make(chan struct{})
	go func() {
		tk := time.NewTicker(tick)
		defer tk.Stop()
		for {
			select {
			case <-tk.C:
				_, _ = s.MarkTimeouts()
			case <-stop:
				return
			}
		}
	}()
	return stop
}

// ---------- 查询 ----------

func loadActivity(t *sql.Tx, id int64) (*model.Activity, error) {
	var a model.Activity
	var approverID sql.NullInt64
	var rejectReason, stopReason sql.NullString
	var pilotJSON string
	err := t.QueryRow(`SELECT id, applicant_id, approver_id, reason, interval, status, reject_reason, pilot_ids, stop_reason, created_at, updated_at
		FROM activities WHERE id=?`, id).
		Scan(&a.ID, &a.ApplicantID, &approverID, &a.Reason, &a.Interval, &a.Status, &rejectReason, &pilotJSON, &stopReason, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.Err("not_found", "活动不存在")
	}
	if err != nil {
		return nil, err
	}
	if approverID.Valid {
		a.ApproverID = &approverID.Int64
	}
	if rejectReason.Valid {
		a.RejectReason = &rejectReason.String
	}
	if stopReason.Valid {
		a.StopReason = &stopReason.String
	}
	_ = json.Unmarshal([]byte(pilotJSON), &a.PilotIDs)
	return &a, nil
}

// GetActivity 查询活动。
func (s *Service) GetActivity(id int64) (*model.Activity, error) {
	var a *model.Activity
	err := tx(s.DB, func(t *sql.Tx) error {
		v, err := loadActivity(t, id)
		a = v
		return err
	})
	return a, err
}

func activityPublishes(t *sql.Tx, activityID int64) ([]model.Publish, error) {
	rows, err := t.Query(`SELECT id, activity_id, vehicle_id, version, target, batch, status, published_at, ack_at, fail_reason, timeout_at, late
		FROM publishes WHERE activity_id=? ORDER BY vehicle_id, version`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Publish
	for rows.Next() {
		var p model.Publish
		var ackAt, failReason, timeoutAt sql.NullString
		var late int
		if err := rows.Scan(&p.ID, &p.ActivityID, &p.VehicleID, &p.Version, &p.Target, &p.Batch, &p.Status, &p.PublishedAt, &ackAt, &failReason, &timeoutAt, &late); err != nil {
			return nil, err
		}
		p.Late = late == 1
		scanOptTime(ackAt, &p.AckAt)
		scanOptTime(timeoutAt, &p.TimeoutAt)
		if failReason.Valid {
			p.FailReason = &failReason.String
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanOptTime(s sql.NullString, dst **string) {
	if s.Valid {
		*dst = &s.String
	}
}

// ListActivities 列出活动；applicantID>0 时仅列本人申请。
func (s *Service) ListActivities(applicantID int64) ([]model.Activity, error) {
	q := `SELECT id, applicant_id, approver_id, reason, interval, status, reject_reason, pilot_ids, stop_reason, created_at, updated_at FROM activities`
	args := []any{}
	if applicantID > 0 {
		q += ` WHERE applicant_id=?`
		args = append(args, applicantID)
	}
	q += ` ORDER BY id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Activity
	for rows.Next() {
		var a model.Activity
		var approverID sql.NullInt64
		var rejectReason, stopReason sql.NullString
		var pilotJSON string
		if err := rows.Scan(&a.ID, &a.ApplicantID, &approverID, &a.Reason, &a.Interval, &a.Status, &rejectReason, &pilotJSON, &stopReason, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if approverID.Valid {
			a.ApproverID = &approverID.Int64
		}
		if rejectReason.Valid {
			a.RejectReason = &rejectReason.String
		}
		if stopReason.Valid {
			a.StopReason = &stopReason.String
		}
		_ = json.Unmarshal([]byte(pilotJSON), &a.PilotIDs)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActivityDetail 核查页数据：活动、逐车下发与生效、回执与审计。
type ActivityDetail struct {
	Activity  *model.Activity
	Publishes []model.Publish
	Receipts  []model.Receipt
	Audits    []model.Audit
	Vehicles  map[string]*model.Vehicle
}

// GetActivityDetail 组装逐车核查数据（发布前值/期望值/生效值/成败/未确认）。
func (s *Service) GetActivityDetail(id int64) (*ActivityDetail, error) {
	d := &ActivityDetail{Vehicles: map[string]*model.Vehicle{}}
	err := tx(s.DB, func(t *sql.Tx) error {
		a, err := loadActivity(t, id)
		if err != nil {
			return err
		}
		d.Activity = a
		if d.Publishes, err = activityPublishes(t, id); err != nil {
			return err
		}
		rows, err := t.Query(`SELECT id, name, online, current_version, current_interval, baseline FROM vehicles ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v model.Vehicle
			var online int
			if err := rows.Scan(&v.ID, &v.Name, &online, &v.CurrentVersion, &v.CurrentInterval, &v.Baseline); err != nil {
				return err
			}
			v.Online = online == 1
			d.Vehicles[v.ID] = &v
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rrows, err := t.Query(`SELECT id, vehicle_id, version, result, accepted, code, note, at FROM receipts ORDER BY id DESC LIMIT 100`)
		if err != nil {
			return err
		}
		defer rrows.Close()
		for rrows.Next() {
			var r model.Receipt
			var acc int
			if err := rrows.Scan(&r.ID, &r.VehicleID, &r.Version, &r.Result, &acc, &r.Code, &r.Note, &r.At); err != nil {
				return err
			}
			r.Accepted = acc == 1
			d.Receipts = append(d.Receipts, r)
		}
		if err := rrows.Err(); err != nil {
			return err
		}
		arows, err := t.Query(`SELECT id, activity_id, actor, action, detail, at FROM audits WHERE activity_id=? ORDER BY id`, id)
		if err != nil {
			return err
		}
		defer arows.Close()
		for arows.Next() {
			var au model.Audit
			if err := arows.Scan(&au.ID, &au.ActivityID, &au.Actor, &au.Action, &au.Detail, &au.At); err != nil {
				return err
			}
			d.Audits = append(d.Audits, au)
		}
		return arows.Err()
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ListReceipts 全部回执/拒绝记录（运营核查）。
func (s *Service) ListReceipts() ([]model.Receipt, error) {
	rows, err := s.DB.Query(`SELECT id, vehicle_id, version, result, accepted, code, note, at FROM receipts ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Receipt
	for rows.Next() {
		var r model.Receipt
		var acc int
		if err := rows.Scan(&r.ID, &r.VehicleID, &r.Version, &r.Result, &acc, &r.Code, &r.Note, &r.At); err != nil {
			return nil, err
		}
		r.Accepted = acc == 1
		out = append(out, r)
	}
	return out, rows.Err()
}
