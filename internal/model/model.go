// Package model 定义题目09的核心领域模型与状态常量。
package model

// Role 预置角色身份（服务端会话绑定，禁止请求参数指定身份）。
type Role string

const (
	RoleApplicant Role = "applicant" // 研发申请人
	RoleApprover  Role = "approver"  // 审批负责人
	RoleOperator  Role = "operator"  // 发布运营
)

// Valid 判断角色是否合法。
func (r Role) Valid() bool {
	return r == RoleApplicant || r == RoleApprover || r == RoleOperator
}

// Label 返回角色中文名。
func (r Role) Label() string {
	switch r {
	case RoleApplicant:
		return "研发申请人"
	case RoleApprover:
		return "审批负责人"
	case RoleOperator:
		return "发布运营"
	}
	return string(r)
}

// 活动状态机。
const (
	ActDraft             = "draft"              // 草稿/被退回，可修改再提交
	ActSubmitted         = "submitted"          // 待审批
	ActRejected          = "rejected"           // 已退回
	ActApproved          = "approved"           // 已批准，待发起试点
	ActPiloting          = "piloting"           // 试点已下发
	ActPilotPaused       = "pilot_paused"       // 试点失败/超时，暂停推进
	ActExpanding         = "expanding"          // 已确认推广，剩余车辆下发中
	ActStopped           = "stopped"            // 已停止（保留已发/未发范围）
	ActCompleted         = "completed"          // 全部目标车辆成功，负责人记完成
	ActRestoreRequested  = "restore_requested"  // 运营发起恢复，待批准
	ActRestoreApproved   = "restore_approved"   // 负责人已批准恢复
	ActRestoring         = "restoring"          // 恢复下发中
	ActRestored          = "restored"           // 恢复完成
)

// 车辆下发记录状态。
const (
	PubPublished = "published" // 已下发，等待回执
	PubSuccess   = "success"   // 收到成功回执（车端生效）
	PubFailed    = "failed"    // 收到失败回执（不改生效值）
	PubTimeout   = "timeout"   // 超时无有效回执（迟到成功可改写并保留历史）
)

// 下发批次。
const (
	BatchPilot   = "pilot"   // 试点批次（2台）
	BatchExpand  = "expand"  // 推广批次（剩余3台）
	BatchRestore = "restore" // 恢复批次（按活动前值）
)

// 回执结果。
const (
	AckSuccess = "success"
	AckFailed  = "failed"
)

// 间隔边界与默认值。
const (
	IntervalMin      = 10
	IntervalMax      = 300
	IntervalDefault  = 60
	PilotCount       = 2 // 试点固定两台
	FleetSize        = 5 // 车队固定五台
)

// User 预置账号。
type User struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Role Role   `json:"role"`
}

// Vehicle 测试车辆。BaselineInterval 为本活动首次下发前的间隔（恢复目标）。
type Vehicle struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Token           string `json:"-"` // 车端令牌，绝不外泄到模板/日志
	Online          bool   `json:"online"`
	CurrentVersion  int    `json:"current_version"`
	CurrentInterval int    `json:"current_interval"` // 已生效值
	Baseline        *int   `json:"baseline"`         // 活动前值（nil=未参与过下发）
}

// Activity 发布活动。时间统一为 RFC3339 字符串（数据库存储格式）。
type Activity struct {
	ID           int64    `json:"id"`
	ApplicantID  int64    `json:"applicant_id"`
	ApproverID   *int64   `json:"approver_id"`
	Reason       string   `json:"reason"`
	Interval     int      `json:"interval"`
	Status       string   `json:"status"`
	RejectReason *string  `json:"reject_reason"`
	PilotIDs     []string `json:"pilot_ids"`
	StopReason   *string  `json:"stop_reason"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

// Publish 单车单版本下发记录：期望值=target，生效值在 Vehicle 上。
type Publish struct {
	ID          int64      `json:"id"`
	ActivityID  int64      `json:"activity_id"`
	VehicleID   string     `json:"vehicle_id"`
	Version     int        `json:"version"`
	Target      int        `json:"target"` // 期望间隔
	Batch       string     `json:"batch"`
	Status      string     `json:"status"`
	PublishedAt string     `json:"published_at"`
	AckAt       *string    `json:"ack_at"`
	FailReason  *string    `json:"fail_reason"`
	TimeoutAt   *string    `json:"timeout_at"` // 超时历史（迟到成功后保留）
	Late        bool       `json:"late"`       // 超时后迟到成功
}

// Receipt 车端回执/拒绝记录（含被拒绝的非法回执，供诊断）。
type Receipt struct {
	ID        int64  `json:"id"`
	VehicleID string `json:"vehicle_id"`
	Version   int    `json:"version"`
	Result    string `json:"result"`
	Accepted  bool   `json:"accepted"`
	Code      string `json:"code"` // ok/duplicate/stale/unknown_version/invalid_result...
	Note      string `json:"note"`
	At        string `json:"at"`
}

// Audit 操作审计，供核查页展示。
type Audit struct {
	ID         int64  `json:"id"`
	ActivityID int64  `json:"activity_id"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	Detail     string `json:"detail"`
	At         string `json:"at"`
}

// BusinessError 业务拒绝，携带用户可读原因（页面展示失败原因用）。
type BusinessError struct {
	Code string
	Msg  string
}

func (e *BusinessError) Error() string { return e.Msg }

func Err(code, msg string) *BusinessError { return &BusinessError{Code: code, Msg: msg} }

// ActLabel 活动状态中文名。
func ActLabel(s string) string {
	m := map[string]string{
		ActDraft: "草稿", ActSubmitted: "待审批", ActRejected: "已退回",
		ActApproved: "待试点", ActPiloting: "试点中", ActPilotPaused: "试点暂停",
		ActExpanding: "推广中", ActStopped: "已停止", ActCompleted: "已完成",
		ActRestoreRequested: "恢复待批", ActRestoreApproved: "恢复已批",
		ActRestoring: "恢复中", ActRestored: "恢复完成",
	}
	if v, ok := m[s]; ok {
		return v
	}
	return s
}

// PubLabel 下发状态中文名。
func PubLabel(s string) string {
	m := map[string]string{
		PubPublished: "已下发", PubSuccess: "已生效", PubFailed: "失败", PubTimeout: "超时",
	}
	if v, ok := m[s]; ok {
		return v
	}
	return s
}

// BatchLabel 批次中文名。
func BatchLabel(b string) string {
	m := map[string]string{BatchPilot: "试点", BatchExpand: "推广", BatchRestore: "恢复"}
	if v, ok := m[b]; ok {
		return v
	}
	return b
}
