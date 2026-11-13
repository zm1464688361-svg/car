package model

import (
	"errors"
	"testing"
)

func TestRoleValidAndLabel(t *testing.T) {
	for _, r := range []Role{RoleApplicant, RoleApprover, RoleOperator} {
		if !r.Valid() {
			t.Fatalf("角色 %s 应合法", r)
		}
		if r.Label() == string(r) {
			t.Fatalf("角色 %s 应有中文标签", r)
		}
	}
	if Role("hacker").Valid() {
		t.Fatal("未知角色应不合法")
	}
	if Role("hacker").Label() != "hacker" {
		t.Fatal("未知角色标签应返回原值")
	}
}

func TestBusinessError(t *testing.T) {
	be := Err("bad_state", "状态错误")
	if be.Code != "bad_state" || be.Msg != "状态错误" {
		t.Fatalf("构造异常: %+v", be)
	}
	if be.Error() == "" {
		t.Fatal("错误信息不应为空")
	}
	var target *BusinessError
	if !errors.As(be, &target) {
		t.Fatal("应可断言为 BusinessError")
	}
}

func TestStatusAndBatchLabels(t *testing.T) {
	for _, s := range []string{ActDraft, ActSubmitted, ActRejected, ActApproved, ActPiloting,
		ActPilotPaused, ActExpanding, ActStopped, ActCompleted, ActRestoreRequested,
		ActRestoreApproved, ActRestoring, ActRestored, "unknown"} {
		if ActLabel(s) == "" {
			t.Fatalf("状态 %s 应有标签", s)
		}
	}
	for _, s := range []string{PubPublished, PubSuccess, PubFailed, PubTimeout, "unknown"} {
		if PubLabel(s) == "" {
			t.Fatalf("下发状态 %s 应有标签", s)
		}
	}
	for _, s := range []string{BatchPilot, BatchExpand, BatchRestore, "unknown"} {
		if BatchLabel(s) == "" {
			t.Fatalf("批次 %s 应有标签", s)
		}
	}
}
