package web

import (
	"net/http"
	"strconv"

	"github.com/zm1464688361-svg/car/internal/model"
)

// handleRoot 按角色跳转工作台。
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	switch u.Role {
	case model.RoleApplicant:
		http.Redirect(w, r, "/applicant", http.StatusFound)
	case model.RoleApprover:
		http.Redirect(w, r, "/approver", http.StatusFound)
	case model.RoleOperator:
		http.Redirect(w, r, "/operator", http.StatusFound)
	default:
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	users, err := s.svc.ListUsers()
	if err != nil {
		http.Error(w, bizMsg(err), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "login.html", "登录 · 车队日志策略发布平台", map[string]any{"Users": users}, http.StatusOK)
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	uid, err := formInt64(r, "uid")
	if err != nil {
		redirectFlash(w, r, "/login", "", bizMsg(err))
		return
	}
	u, err := s.svc.GetUser(uid)
	if err != nil {
		redirectFlash(w, r, "/login", "", bizMsg(err))
		return
	}
	s.setSession(w, u.ID)
	s.handleRoot(w, r)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusFound)
}

// handleApplicant 研发申请人工作台。
func (s *Server) handleApplicant(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleApplicant)
	if u == nil {
		return
	}
	acts, err := s.svc.ListActivities(u.ID)
	if err != nil {
		http.Error(w, bizMsg(err), http.StatusInternalServerError)
		return
	}
	editID := r.URL.Query().Get("edit")
	var edit *model.Activity
	if editID != "" {
		if id, err := strconv.ParseInt(editID, 10, 64); err == nil {
			for i := range acts {
				if acts[i].ID == id {
					edit = &acts[i]
				}
			}
		}
	}
	s.render(w, r, "applicant.html", "研发申请 · 车队日志策略发布平台", map[string]any{
		"Activities": acts,
		"Edit":       edit,
	}, http.StatusOK)
}

// handleApprover 审批负责人工作台。
func (s *Server) handleApprover(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleApprover)
	if u == nil {
		return
	}
	acts, err := s.svc.ListActivities(0)
	if err != nil {
		http.Error(w, bizMsg(err), http.StatusInternalServerError)
		return
	}
	var submitted, paused, expanding, restoreReq, restoring []model.Activity
	for i := range acts {
		switch acts[i].Status {
		case model.ActSubmitted:
			submitted = append(submitted, acts[i])
		case model.ActPilotPaused:
			paused = append(paused, acts[i])
		case model.ActExpanding:
			expanding = append(expanding, acts[i])
		case model.ActRestoreRequested:
			restoreReq = append(restoreReq, acts[i])
		case model.ActRestoring:
			restoring = append(restoring, acts[i])
		}
	}
	s.render(w, r, "approver.html", "审批核查 · 车队日志策略发布平台", map[string]any{
		"Submitted":  submitted,
		"Paused":     paused,
		"Expanding":  expanding,
		"RestoreReq": restoreReq,
		"Restoring":  restoring,
		"Activities": acts,
	}, http.StatusOK)
}

// handleOperator 发布运营工作台。
func (s *Server) handleOperator(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleOperator)
	if u == nil {
		return
	}
	acts, err := s.svc.ListActivities(0)
	if err != nil {
		http.Error(w, bizMsg(err), http.StatusInternalServerError)
		return
	}
	vehicles, err := s.svc.ListVehicles()
	if err != nil {
		http.Error(w, bizMsg(err), http.StatusInternalServerError)
		return
	}
	var approved, piloting, paused, expanding, stopped, restoreApproved []model.Activity
	for i := range acts {
		switch acts[i].Status {
		case model.ActApproved:
			approved = append(approved, acts[i])
		case model.ActPiloting:
			piloting = append(piloting, acts[i])
		case model.ActPilotPaused:
			paused = append(paused, acts[i])
		case model.ActExpanding:
			expanding = append(expanding, acts[i])
		case model.ActStopped:
			stopped = append(stopped, acts[i])
		case model.ActRestoreApproved:
			restoreApproved = append(restoreApproved, acts[i])
		}
	}
	s.render(w, r, "operator.html", "发布运营 · 车队日志策略发布平台", map[string]any{
		"Approved":        approved,
		"Piloting":        piloting,
		"Paused":          paused,
		"Expanding":       expanding,
		"Stopped":         stopped,
		"RestoreApproved": restoreApproved,
		"Activities":      acts,
		"Vehicles":        vehicles,
	}, http.StatusOK)
}

// handleActivity 逐车核查页。
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r)
	if u == nil {
		return
	}
	id, err := formInt64Path(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d, err := s.svc.GetActivityDetail(id)
	if err != nil {
		http.Error(w, bizMsg(err), http.StatusNotFound)
		return
	}
	s.render(w, r, "activity.html", "活动核查 #"+strconv.FormatInt(id, 10), map[string]any{
		"D": d,
	}, http.StatusOK)
}

func formInt64Path(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

// handleSimulator 车辆模拟器（模拟车端拉取与回执）。
func (s *Server) handleSimulator(w http.ResponseWriter, r *http.Request) {
	u := s.requireRole(w, r, model.RoleOperator)
	if u == nil {
		return
	}
	vehicles, err := s.svc.ListVehicles()
	if err != nil {
		http.Error(w, bizMsg(err), http.StatusInternalServerError)
		return
	}
	receipts, err := s.svc.ListReceipts()
	if err != nil {
		http.Error(w, bizMsg(err), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "simulator.html", "车辆模拟器 · 车队日志策略发布平台", map[string]any{
		"Vehicles": vehicles,
		"Receipts": receipts,
	}, http.StatusOK)
}
