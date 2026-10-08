package manage

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	servertypes "github.com/digitalwayhk/core/pkg/server/types"
)

func TestBillApproveHiddenOutsideDryRun(t *testing.T) {
	dir := t.TempDir()
	for _, mode := range []string{"testnet", "live", "mainnet"} {
		path := writeModeConfig(t, dir, mode)
		t.Setenv("OPERATOR_CONFIG", path)
		if strings.Contains(routerPaths(NewBillManage().Routers()), "billapprove") {
			t.Fatalf("%s still registers billapprove", mode)
		}
		_, err := (&BillApprove{Code: "bill-x"}).Do(nil)
		if err == nil || !strings.Contains(err.Error(), "CLI-only") {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}

func TestBillApproveDryRunRejectsLANAndOmitsRealMoneyConfirm(t *testing.T) {
	source, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(filepath.Join(source, "db"))
	t.Cleanup(func() {
		if _, statErr := os.Stat(filepath.Join(source, "db")); !os.IsNotExist(statErr) {
			t.Errorf("sqlite left in %s/db", source)
		}
	})
	t.Chdir(t.TempDir())
	path := writeModeConfig(t, t.TempDir(), "dry-run")
	t.Setenv("OPERATOR_CONFIG", path)
	if !strings.Contains(routerPaths(NewBillManage().Routers()), "billapprove") {
		t.Fatal("dry-run hid bill approve")
	}
	req := &http.Request{RemoteAddr: "172.30.0.2:54321", Header: make(http.Header)}
	_, err = (&BillApprove{Code: "bill-x"}).Do(loopbackRequest{ip: "127.0.0.1", req: req})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	_, err = (&BillApprove{Code: "missing-bill"}).Do(loopbackRequest{ip: "127.0.0.1", req: &http.Request{RemoteAddr: "127.0.0.1:9", Header: make(http.Header)}})
	if err == nil || strings.Contains(err.Error(), "loopback") || strings.Contains(err.Error(), "i-understand-real-money") {
		t.Fatal(err)
	}
}

func TestBillApproveHiddenWhenConfigMissing(t *testing.T) {
	t.Setenv("OPERATOR_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	if strings.Contains(routerPaths(NewBillManage().Routers()), "billapprove") {
		t.Fatal("missing config still exposes bill approve")
	}
}

func TestBillCloseAndReopenRejectNonLoopback(t *testing.T) {
	req := &http.Request{RemoteAddr: "172.30.0.2:9", Header: make(http.Header)}
	caller := loopbackRequest{ip: "127.0.0.1", req: req}
	if _, err := (&BillReopen{Code: "bill-x"}).Do(caller); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	if _, err := (&BillClose{Code: "bill-x"}).Do(caller); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	if _, err := (&BillClose{Code: "bill-x"}).Do(nil); err == nil {
		t.Fatal("nil caller closed a bill")
	}
}

func TestApprovalSettlementRejectsNonLoopback(t *testing.T) {
	req := &http.Request{RemoteAddr: "172.30.0.2:9", Header: http.Header{"X-Forwarded-For": []string{"127.0.0.1"}}}
	caller := loopbackRequest{ip: "127.0.0.1", req: req}
	if _, err := (&Approve{RequestID: "1"}).Do(caller); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	if _, err := (&Reject{RequestID: "1"}).Do(caller); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	if _, err := (&Approve{RequestID: "1"}).Do(nil); err == nil {
		t.Fatal("nil caller approved")
	}
}

func writeModeConfig(t *testing.T, dir, mode string) string {
	t.Helper()
	chain := "5042002"
	driver := "mock"
	if mode == "mainnet" {
		chain = "5042"
		driver = "rpc"
	}
	if mode == "testnet" || mode == "live" {
		driver = "rpc"
	}
	body := "mode: " + mode + "\nchainID: \"" + chain + "\"\nvault: \"0x1111111111111111111111111111111111111111\"\nagent: \"0x2222222222222222222222222222222222222222\"\nchainDriver: " + driver + "\nreserveFloorUSDC: \"0\"\nreserveTargetUSDC: \"0\"\n"
	path := filepath.Join(dir, mode+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func routerPaths(routes []servertypes.IRouter) string {
	var paths []string
	for _, route := range routes {
		info := route.RouterInfo()
		if info != nil {
			paths = append(paths, info.Path)
		}
	}
	return strings.Join(paths, "\n")
}

type loopbackRequest struct {
	ip  string
	req *http.Request
}

func (r loopbackRequest) GetTraceId() string        { return "" }
func (r loopbackRequest) GetUser() (string, string) { return "", "" }
func (r loopbackRequest) GetClientIP() string       { return r.ip }
func (r loopbackRequest) NewID() uint               { return 0 }
func (r loopbackRequest) Authorized() bool          { return true }
func (r loopbackRequest) CallService(servertypes.IRouter, ...func(servertypes.IResponse)) (servertypes.IResponse, error) {
	return nil, nil
}
func (r loopbackRequest) CallTargetService(servertypes.IRouter, *servertypes.TargetInfo, ...func(servertypes.IResponse)) (servertypes.IResponse, error) {
	return nil, nil
}
func (r loopbackRequest) GetValue(string) string                               { return "" }
func (r loopbackRequest) Bind(interface{}) error                               { return nil }
func (r loopbackRequest) GoZeroBind(interface{}) error                         { return nil }
func (r loopbackRequest) NewResponse(interface{}, error) servertypes.IResponse { return nil }
func (r loopbackRequest) GetPath() string                                      { return "" }
func (r loopbackRequest) GetClaims(string) interface{}                         { return nil }
func (r loopbackRequest) ServiceName() string                                  { return "" }
func (r loopbackRequest) GetServerInfo() *servertypes.TargetInfo               { return nil }
func (r loopbackRequest) GetTargetServerInfo(string) *servertypes.TargetInfo {
	return nil
}
func (r loopbackRequest) GetHttpRequest() *http.Request { return r.req }
