package treasury

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestMainnetPayBroadcastGate(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	vault := common.HexToAddress("0x2222222222222222222222222222222222222222")
	agent := crypto.PubkeyToAddress(key.PublicKey)
	srv, hits := newVaultRPC(t, vault, key)
	defer srv.Close()

	opt := LiveOptions{
		RPC: srv.URL, Mode: "mainnet", ChainID: big.NewInt(5042),
		Vault: vault, Agent: agent, USDC: common.HexToAddress("0x3600000000000000000000000000000000000000"),
		AgentKey: key, GasGwei: 50,
	}
	ctx := context.Background()
	call := PayCall{
		Category: "domains", Payee: "0x3333333333333333333333333333333333333333",
		Amount: big.NewInt(1_000_000), DecisionHash: common.HexToHash("0x01"),
	}

	t.Setenv("CONFIRM_MAINNET", "")
	dry, err := DialLive(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	res, err := dry.Pay(ctx, call)
	dry.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "dry_run_paid" || res.TxHash != "" || !onlyCalls(hits.methods(), "eth_chainId", "eth_call") {
		t.Fatalf("status %s tx %q methods %v", res.Status, res.TxHash, hits.methods())
	}

	hits.reset()
	opt.Broadcast = true
	refused, err := DialLive(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refused.Pay(ctx, call); err == nil || !strings.Contains(err.Error(), "CONFIRM_MAINNET") {
		refused.Close()
		t.Fatalf("got %v methods %v", err, hits.methods())
	}
	refused.Close()
	if containsMethod(hits.methods(), "eth_sendRawTransaction") {
		t.Fatalf("sent without confirm: %v", hits.methods())
	}

	hits.reset()
	t.Setenv("CONFIRM_MAINNET", "1")
	live, err := DialLive(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	paid, err := live.Pay(ctx, call)
	live.Close()
	if err != nil {
		t.Fatalf("%v methods %v", err, hits.methods())
	}
	if paid.Status != "paid" || !realHash(paid.TxHash) || !containsMethod(hits.methods(), "eth_sendRawTransaction") {
		t.Fatalf("%+v methods %v", paid, hits.methods())
	}
}

func TestPayKeepsHashWhenReceiptWaitFails(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	vault := common.HexToAddress("0x2222222222222222222222222222222222222222")
	srv, hits := newVaultRPC(t, vault, key)
	defer srv.Close()
	hits.failReceipt = true
	t.Setenv("CONFIRM_MAINNET", "1")
	ctx := context.Background()
	live, err := DialLive(ctx, LiveOptions{
		RPC: srv.URL, Mode: "mainnet", Broadcast: true, ChainID: big.NewInt(5042),
		Vault: vault, Agent: crypto.PubkeyToAddress(key.PublicKey),
		USDC:     common.HexToAddress("0x3600000000000000000000000000000000000000"),
		AgentKey: key, GasGwei: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	res, err := live.Pay(ctx, PayCall{
		Category: "domains", Payee: "0x3333333333333333333333333333333333333333",
		Amount: big.NewInt(1_000_000), DecisionHash: common.HexToHash("0x01"),
	})
	if err == nil || !realHash(res.TxHash) || !containsMethod(hits.methods(), "eth_sendRawTransaction") {
		t.Fatalf("%+v err %v methods %v", res, err, hits.methods())
	}
}

func realHash(tx string) bool {
	tx = strings.TrimSpace(tx)
	if len(tx) != 66 || !strings.HasPrefix(tx, "0x") {
		return false
	}
	return common.HexToHash(tx) != (common.Hash{})
}

type rpcHits struct {
	mu          sync.Mutex
	seen        []string
	vault       common.Address
	failReceipt bool
}

func (h *rpcHits) add(method string) {
	h.mu.Lock()
	h.seen = append(h.seen, method)
	h.mu.Unlock()
}

func (h *rpcHits) methods() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]string(nil), h.seen...)
	return out
}

func (h *rpcHits) reset() {
	h.mu.Lock()
	h.seen = nil
	h.mu.Unlock()
}

func onlyCalls(methods []string, allow ...string) bool {
	ok := map[string]bool{}
	for _, m := range allow {
		ok[m] = true
	}
	if len(methods) == 0 {
		return false
	}
	for _, m := range methods {
		if !ok[m] {
			return false
		}
	}
	return true
}

func containsMethod(methods []string, want string) bool {
	for _, m := range methods {
		if m == want {
			return true
		}
	}
	return false
}

func newVaultRPC(t *testing.T, vault common.Address, _ *ecdsa.PrivateKey) (*httptest.Server, *rpcHits) {
	t.Helper()
	hits := &rpcHits{vault: vault}
	receipts := map[string]json.RawMessage{}
	var mu sync.Mutex
	payID := contractABI.Methods["pay"].ID
	paidTopic := contractABI.Events["AgentPaid"].ID
	bloom := "0x" + strings.Repeat("00", 256)
	paidResult := "0x" + strings.Repeat("0", 63) + "1" + strings.Repeat("0", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		hits.add(req.Method)
		var result any
		switch req.Method {
		case "eth_chainId":
			result = "0x13b2"
		case "eth_call":
			result = paidResult
		case "eth_estimateGas":
			result = "0x186a0"
		case "eth_getTransactionCount":
			result = "0x0"
		case "eth_gasPrice":
			result = "0x4a817c800"
		case "eth_sendRawTransaction":
			var hexTx string
			_ = json.Unmarshal(req.Params[0], &hexTx)
			var tx types.Transaction
			if err := tx.UnmarshalBinary(common.FromHex(hexTx)); err != nil {
				writeRPCError(w, req.ID, err.Error())
				return
			}
			hash := tx.Hash()
			logs := []any{}
			if len(tx.Data()) >= 4 && string(tx.Data()[:4]) == string(payID) && len(tx.Data()) >= 32 {
				decision := common.BytesToHash(tx.Data()[len(tx.Data())-32:])
				logs = []any{map[string]any{
					"address": vault.Hex(), "data": "0x",
					"topics":          []string{paidTopic.Hex(), common.Hash{}.Hex(), common.Hash{}.Hex(), decision.Hex()},
					"transactionHash": hash.Hex(), "blockNumber": "0x1", "logIndex": "0x0",
				}}
			}
			body, _ := json.Marshal(map[string]any{
				"type": "0x0", "status": "0x1", "cumulativeGasUsed": "0x5208", "gasUsed": "0x5208",
				"logsBloom": bloom, "logs": logs, "transactionHash": hash.Hex(),
				"blockHash": "0x" + strings.Repeat("11", 32), "blockNumber": "0x1", "transactionIndex": "0x0",
			})
			mu.Lock()
			receipts[strings.ToLower(hash.Hex())] = body
			mu.Unlock()
			result = hash.Hex()
		case "eth_getTransactionReceipt":
			if hits.failReceipt {
				writeRPCError(w, req.ID, "receipt unavailable")
				return
			}
			var hexHash string
			_ = json.Unmarshal(req.Params[0], &hexHash)
			mu.Lock()
			raw, ok := receipts[strings.ToLower(hexHash)]
			mu.Unlock()
			if !ok {
				result = nil
				break
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID, "result": json.RawMessage(raw),
			})
			return
		default:
			writeRPCError(w, req.ID, "unexpected "+req.Method)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	return srv, hits
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": msg},
	})
}
