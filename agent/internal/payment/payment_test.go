package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func invoice(t *testing.T) Intent {
	t.Helper()
	in, err := Prepare(Request{"invoice-1", "infra", "0x0000000000000000000000000000000000000003", "0.50"}, big.NewInt(31337), common.HexToAddress("0x1"), common.HexToAddress("0x2"))
	if err != nil {
		t.Fatal(err)
	}
	return in
}
func TestAmountUsesSixDecimals(t *testing.T) {
	for input, want := range map[string]string{"0.5": "500000", "1": "1000000", "0.000001": "1", "1.234567": "1234567"} {
		n, err := ParseUSDC(input)
		if err != nil || n.String() != want {
			t.Fatalf("%s: %v %v", input, n, err)
		}
	}
	for _, input := range []string{"0", "-1", "1e3", "0.0000001", "01", "NaN", "1.", " 1"} {
		if _, err := ParseUSDC(input); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
func TestStableInvoiceIdentity(t *testing.T) {
	first := invoice(t)
	req := Request{"invoice-1", "infra", first.Payee.Hex(), "0.500000"}
	same, err := Prepare(req, big.NewInt(31337), first.Vault, first.Agent)
	if err != nil || same != first {
		t.Fatal("equivalent amount altered identity")
	}
	req.AmountUSDC = "1"
	changed, _ := Prepare(req, big.NewInt(31337), first.Vault, first.Agent)
	if changed.DecisionHash != first.DecisionHash {
		t.Fatal("changed terms minted a second invoice hash")
	}
	scoped, _ := Prepare(req, big.NewInt(5042002), first.Vault, first.Agent)
	if scoped.DecisionHash == first.DecisionHash {
		t.Fatal("chain scope collision")
	}
	rotated, _ := Prepare(req, big.NewInt(31337), first.Vault, common.HexToAddress("0x4"))
	if rotated.DecisionHash != first.DecisionHash {
		t.Fatal("agent rotation minted a new invoice identity")
	}

	data, err := Calldata(first)
	if err != nil || len(data) != 132 {
		t.Fatalf("bad pay calldata: %v", err)
	}
}

type fakeBackend struct {
	sends     int
	consumed  bool
	waitError bool
	sendError bool
	pending   bool
	status    string
}

func (f *fakeBackend) Recover(_ context.Context, in Intent) (*Result, error) {
	if !f.consumed {
		return nil, nil
	}
	return &Result{Status: f.status, DecisionHash: in.DecisionHash, Recovered: true, RequestID: "1"}, nil
}
func (f *fakeBackend) Send(context.Context, Intent) (common.Hash, error) {
	f.sends++
	if f.sendError {
		return common.Hash{}, fmt.Errorf("response lost")
	}
	if !f.pending {
		f.consumed = true
	}
	return common.HexToHash("0x123"), nil
}
func (f *fakeBackend) Wait(_ context.Context, in Intent, hash common.Hash) (*Result, error) {
	if f.waitError {
		return nil, context.DeadlineExceeded
	}
	f.consumed = true
	return &Result{Status: f.status, DecisionHash: in.DecisionHash, TxHash: hash}, nil
}

func TestRestartDoesNotPayOrEscalateTwice(t *testing.T) {
	for _, status := range []string{"paid", "approval_requested"} {
		t.Run(status, func(t *testing.T) {
			dir := t.TempDir()
			in := invoice(t)
			backend := &fakeBackend{status: status}
			if _, err := Execute(context.Background(), dir, in, backend); err != nil {
				t.Fatal(err)
			}
			result, err := Execute(context.Background(), dir, in, backend)
			if err != nil || !result.Recovered || backend.sends != 1 {
				t.Fatalf("restart: %v sends %d", err, backend.sends)
			}
			os.Remove(filepath.Join(dir, in.DecisionHash.Hex()+".json"))
			if _, err := Execute(context.Background(), dir, in, backend); err != nil || backend.sends != 1 {
				t.Fatal("journal loss duplicated payment")
			}
		})
	}
}
func TestMinedBeforeJournalRecovery(t *testing.T) {
	dir := t.TempDir()
	in := invoice(t)
	b := &fakeBackend{status: "paid", waitError: true}
	if _, err := Execute(context.Background(), dir, in, b); err == nil {
		t.Fatal("expected interrupted wait")
	}
	if r, err := Execute(context.Background(), dir, in, b); err != nil || !r.Recovered || b.sends != 1 {
		t.Fatal("mined payment was resent")
	}
}
func TestPendingReceiptResumesWithoutResend(t *testing.T) {
	dir := t.TempDir()
	in := invoice(t)
	b := &fakeBackend{status: "paid", waitError: true, pending: true}
	Execute(context.Background(), dir, in, b)
	b.waitError = false
	if _, err := Execute(context.Background(), dir, in, b); err != nil || b.sends != 1 {
		t.Fatalf("pending resend: %v", err)
	}
}
func TestUnknownSendFailsClosed(t *testing.T) {
	dir := t.TempDir()
	in := invoice(t)
	b := &fakeBackend{sendError: true}
	Execute(context.Background(), dir, in, b)
	if _, err := Execute(context.Background(), dir, in, b); err == nil || !strings.Contains(err.Error(), "unknown") || b.sends != 1 {
		t.Fatalf("ambiguous send retried: %v", err)
	}
}
func TestChangedInvoiceAndResetChainRefused(t *testing.T) {
	dir := t.TempDir()
	in := invoice(t)
	b := &fakeBackend{status: "paid"}
	Execute(context.Background(), dir, in, b)
	changed := in
	changed.Amount = "1000000"
	if _, err := Execute(context.Background(), dir, changed, b); err == nil {
		t.Fatal("changed invoice accepted")
	}
	b.consumed = false
	if _, err := Execute(context.Background(), dir, in, b); err == nil || b.sends != 1 {
		t.Fatal("reset chain replayed completed journal")
	}
}
func TestCorruptJournalRefused(t *testing.T) {
	dir := t.TempDir()
	in := invoice(t)
	os.WriteFile(filepath.Join(dir, in.DecisionHash.Hex()+".json"), []byte("{"), 0600)
	b := &fakeBackend{}
	if _, err := Execute(context.Background(), dir, in, b); err == nil || b.sends != 0 {
		t.Fatal("corrupt journal accepted")
	}
}
func TestRecoveredEventValidatesInvoiceTerms(t *testing.T) {
	in := invoice(t)
	amount, _ := new(big.Int).SetString(in.Amount, 10)
	data, _ := contractABI.Events["AgentPaid"].Inputs.NonIndexed().Pack(amount, uint64(0))
	log := types.Log{Address: in.Vault, Topics: []common.Hash{contractABI.Events["AgentPaid"].ID, common.Hash(in.Category), common.BytesToHash(in.Payee.Bytes()), in.DecisionHash}, Data: data}
	if r, match, err := decodeEvent(in, log); err != nil || !match || r.Status != "paid" {
		t.Fatalf("bad payment decode %v", err)
	}
	in.Amount = "123"
	if _, _, err := decodeEvent(in, log); err == nil {
		t.Fatal("changed lost-journal invoice not detected")
	}
}
func TestLocalEndpointRejectsPublicURLs(t *testing.T) {
	for _, u := range []string{"https://rpc.testnet.arc.io", "http://localhost:8545", "http://127.0.0.1", "http://127.0.0.1:8545?x=1", "http://user:secret@127.0.0.1:8545", "https://127.0.0.1:8545"} {
		if LocalEndpoint(u) {
			t.Fatalf("accepted %s", u)
		}
	}
	for _, u := range []string{"http://127.0.0.1:8545", "http://[::1]:8545"} {
		if !LocalEndpoint(u) {
			t.Fatalf("rejected %s", u)
		}
	}
}
func TestJournalContainsCanonicalRequest(t *testing.T) {
	in := invoice(t)
	dir := t.TempDir()
	b := &fakeBackend{status: "paid"}
	Execute(context.Background(), dir, in, b)
	raw, err := os.ReadFile(filepath.Join(dir, in.DecisionHash.Hex()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var j journal
	if err := json.Unmarshal(raw, &j); err != nil || j.Intent != in || j.Stage != "complete" {
		t.Fatal("journal not durable")
	}
}
