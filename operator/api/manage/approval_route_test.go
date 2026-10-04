package manage

import (
	"strings"
	"testing"
)

func TestApprovalCommandsRegister(t *testing.T) {
	m := NewApprovalManage()
	var paths []string
	for _, r := range m.Routers() {
		info := r.RouterInfo()
		if info == nil || info.Path == "" {
			t.Fatal("missing route")
		}
		paths = append(paths, info.Path)
	}
	joined := strings.Join(paths, "\n")
	for _, needle := range []string{
		"/api/manage/operator/approvalmanage/approve",
		"/api/manage/operator/approvalmanage/reject",
	} {
		if !strings.Contains(joined, needle) {
			t.Fatalf("missing %s in\n%s", needle, joined)
		}
	}
}
