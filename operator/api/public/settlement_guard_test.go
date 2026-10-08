package public

import (
	"net/http"
	"strings"
	"testing"

	servertypes "github.com/digitalwayhk/core/pkg/server/types"
)

func TestStateChangesRejectNonLoopback(t *testing.T) {
	req := &http.Request{RemoteAddr: "172.30.0.2:9", Header: http.Header{"X-Forwarded-For": []string{"127.0.0.1"}}}
	caller := loopbackRequest{ip: "127.0.0.1", req: req}
	if _, err := (&RunOnce{}).Do(caller); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	if _, err := (&RecordRevenue{Ref: "wire-1", From: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", AmountUSDC: "1.00"}).Do(caller); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	if _, err := (&CCTPIn{SourceDomain: "6", TxHash: "0xabc"}).Do(caller); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	hook := &GatewayHook{}
	hook.raw = []byte(`{"type":"gateway.deposit.finalized"}`)
	if _, err := hook.Do(caller); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	if _, err := (&RunOnce{}).Do(nil); err == nil {
		t.Fatal("nil caller ran a cycle")
	}
}

func TestPublicSettlementRejectsNonLoopback(t *testing.T) {
	req := &http.Request{
		RemoteAddr: "172.30.0.2:9",
		Header:     http.Header{"X-Real-Ip": []string{"127.0.0.1"}},
	}
	caller := loopbackRequest{ip: "127.0.0.1", req: req}
	if _, err := (&Approve{RequestID: "1"}).Do(caller); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	if _, err := (&Reject{RequestID: "1"}).Do(caller); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
	if _, err := (&Approve{}).Do(loopbackRequest{}); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatal(err)
	}
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
func (r loopbackRequest) GetTargetServerInfo(string) *servertypes.TargetInfo   { return nil }
func (r loopbackRequest) GetHttpRequest() *http.Request                        { return r.req }
