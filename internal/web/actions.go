package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/zm1464688361-svg/car/internal/model"
)

// --- 申请侧动作（研发申请人） ---

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleApplicant)
	if u == nil {
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	interval, err := formInt(r, "interval")
	if err != nil {
		redirectFlash(w, r, "/applicant", "", bizMsg(err))
		return
	}
	id, err := s.svc.CreateApplication(u.ID, reason, interval)
	if err != nil {
		redirectFlash(w, r, "/applicant", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/applicant", "申请 #"+strconv.FormatInt(id, 10)+" 已创建，请提交审批", "")
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleApplicant)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/applicant", "", bizMsg(err))
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	interval, err := formInt(r, "interval")
	if err != nil {
		redirectFlash(w, r, "/applicant", "", bizMsg(err))
		return
	}
	resubmit := r.FormValue("resubmit") == "1"
	err = s.svc.UpdateApplication(u.ID, id, reason, interval, resubmit)
	if err != nil {
		redirectFlash(w, r, "/applicant", "", bizMsg(err))
		return
	}
	if resubmit {
		redirectFlash(w, r, "/applicant", "申请已修改并重新提交审批", "")
	} else {
		redirectFlash(w, r, "/applicant", "申请已保存为草稿", "")
	}
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleApplicant)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/applicant", "", bizMsg(err))
		return
	}
	if err := s.svc.SubmitApplication(u.ID, id); err != nil {
		redirectFlash(w, r, "/applicant", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/applicant", "申请已提交审批", "")
}

// --- 审批动作（负责人） ---

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleApprover)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/approver", "", bizMsg(err))
		return
	}
	if err := s.svc.ApproveActivity(u.ID, id); err != nil {
		redirectFlash(w, r, "/approver", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/approver", "申请 #"+strconv.FormatInt(id, 10)+" 已批准，等待运营发起试点", "")
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleApprover)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/approver", "", bizMsg(err))
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	if err := s.svc.RejectActivity(u.ID, id, reason); err != nil {
		redirectFlash(w, r, "/approver", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/approver", "申请已退回", "")
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleApprover)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/approver", "", bizMsg(err))
		return
	}
	if err := s.svc.CompleteActivity(u.ID, id); err != nil {
		redirectFlash(w, r, "/approver", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/approver", "活动 #"+strconv.FormatInt(id, 10)+" 已记为完成", "")
}

func (s *Server) handleApproveRestore(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleApprover)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/approver", "", bizMsg(err))
		return
	}
	if err := s.svc.ApproveRestore(u.ID, id); err != nil {
		redirectFlash(w, r, "/approver", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/approver", "恢复已批准，等待运营执行", "")
}

func (s *Server) handleMarkRestored(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleApprover)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/approver", "", bizMsg(err))
		return
	}
	if err := s.svc.MarkRestored(u.ID, id); err != nil {
		redirectFlash(w, r, "/approver", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/approver", "活动 #"+strconv.FormatInt(id, 10)+" 已记为恢复完成", "")
}

// --- 发布动作（运营） ---

func (s *Server) handlePilot(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleOperator)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/operator", "", bizMsg(err))
		return
	}
	pilot := r.Form["vehicle"]
	if err := s.svc.StartPilot(u.ID, id, pilot); err != nil {
		redirectFlash(w, r, "/operator", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/operator", "试点已下发，请到车辆模拟器拉取并回执", "")
}

func (s *Server) handleExpand(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleOperator)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/operator", "", bizMsg(err))
		return
	}
	if err := s.svc.ConfirmExpand(u.ID, id); err != nil {
		redirectFlash(w, r, "/operator", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/operator", "已推广至剩余三台车", "")
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleOperator, model.RoleApprover)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/operator", "", bizMsg(err))
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	if err := s.svc.StopActivity(u.ID, id, reason); err != nil {
		redirectFlash(w, r, "/operator", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/operator", "活动已停止（已下发与已生效记录保留）", "")
}

func (s *Server) handleRequestRestore(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleOperator)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/operator", "", bizMsg(err))
		return
	}
	if err := s.svc.RequestRestore(u.ID, id); err != nil {
		redirectFlash(w, r, "/operator", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/operator", "恢复申请已提交，等待负责人批准", "")
}

func (s *Server) handleExecuteRestore(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleOperator)
	if u == nil {
		return
	}
	id, err := formInt64(r, "id")
	if err != nil {
		redirectFlash(w, r, "/operator", "", bizMsg(err))
		return
	}
	if err := s.svc.ExecuteRestore(u.ID, id); err != nil {
		redirectFlash(w, r, "/operator", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/operator", "恢复版本已下发，等待车辆回执", "")
}

// --- 车辆模拟器动作 ---

func (s *Server) handleSimOnline(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleOperator)
	if u == nil {
		return
	}
	vid := r.FormValue("vehicle")
	online := r.FormValue("online") == "1"
	if err := s.svc.SetVehicleOnline(vid, online); err != nil {
		redirectFlash(w, r, "/simulator", "", bizMsg(err))
		return
	}
	state := "离线"
	if online {
		state = "在线"
	}
	redirectFlash(w, r, "/simulator", "车辆 "+vid+" 已切换为"+state, "")
}

func (s *Server) handleSimPull(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleOperator)
	if u == nil {
		return
	}
	token := r.FormValue("token")
	p, err := s.svc.VehiclePull(token)
	if err != nil {
		redirectFlash(w, r, "/simulator", "", bizMsg(err))
		return
	}
	redirectFlash(w, r, "/simulator",
		"车端拉取成功：版本 v"+strconv.Itoa(p.Version)+"，目标间隔 "+strconv.Itoa(p.Target)+" 秒", "")
}

func (s *Server) handleSimAck(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleOperator)
	if u == nil {
		return
	}
	token := r.FormValue("token")
	version, err := formInt(r, "version")
	if err != nil {
		redirectFlash(w, r, "/simulator", "", bizMsg(err))
		return
	}
	result := r.FormValue("result")
	rec, err := s.svc.VehicleAck(token, version, result)
	if err != nil {
		redirectFlash(w, r, "/simulator", "", bizMsg(err))
		return
	}
	state := "已受理"
	if !rec.Accepted {
		state = "已拒绝"
	}
	redirectFlash(w, r, "/simulator",
		"回执"+state+"（"+rec.Code+"）： "+rec.Note, "")
}
