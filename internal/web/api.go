package web

import (
	"encoding/json"
	"net/http"
)

// 车端模拟 API：令牌放 Header X-Vehicle-Token，供测试脚本与 curl 触发场景。

type apiPullResp struct {
	Version int    `json:"version"`
	Target  int    `json:"target_interval"`
	Batch   string `json:"batch"`
	Status  string `json:"status"`
}

type apiAckReq struct {
	Version int    `json:"version"`
	Result  string `json:"result"`
}

type apiAckResp struct {
	Accepted bool   `json:"accepted"`
	Code     string `json:"code"`
	Note     string `json:"note"`
}

func (s *Server) handleAPIPull(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Vehicle-Token")
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "缺少 X-Vehicle-Token"})
		return
	}
	p, err := s.svc.VehiclePull(token)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": bizMsg(err)})
		return
	}
	writeJSON(w, http.StatusOK, apiPullResp{
		Version: p.Version, Target: p.Target, Batch: p.Batch, Status: p.Status,
	})
}

func (s *Server) handleAPIAck(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Vehicle-Token")
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "缺少 X-Vehicle-Token"})
		return
	}
	var req apiAckReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是 JSON"})
		return
	}
	rec, err := s.svc.VehicleAck(token, req.Version, req.Result)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": bizMsg(err)})
		return
	}
	writeJSON(w, http.StatusOK, apiAckResp{Accepted: rec.Accepted, Code: rec.Code, Note: rec.Note})
}
