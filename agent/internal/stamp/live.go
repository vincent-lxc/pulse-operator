package stamp

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	ExplorerTxBase = "https://testnet.monadvision.com/tx/"
	StampedEvent   = "Stamped(uint256,bytes32,uint256,uint256,uint8,address,uint64,string)"
)

const contractABIJSON = `[
  {"type":"function","name":"stamp","stateMutability":"nonpayable","inputs":[{"name":"decisionHash","type":"bytes32"},{"name":"symbolId","type":"uint256"},{"name":"sizeHint","type":"uint256"},{"name":"action","type":"uint8"},{"name":"note","type":"string"}],"outputs":[{"name":"id","type":"uint256"}]},
  {"type":"function","name":"getReceipt","stateMutability":"view","inputs":[{"name":"id","type":"uint256"}],"outputs":[{"name":"","type":"tuple","components":[
    {"name":"decisionHash","type":"bytes32"},
    {"name":"symbolId","type":"uint256"},
    {"name":"sizeHint","type":"uint256"},
    {"name":"action","type":"uint8"},
    {"name":"note","type":"string"},
    {"name":"stampedAt","type":"uint64"},
    {"name":"stamper","type":"address"}
  ]}]},
  {"type":"event","name":"Stamped","inputs":[
    {"name":"id","type":"uint256","indexed":true},
    {"name":"decisionHash","type":"bytes32","indexed":true},
    {"name":"symbolId","type":"uint256","indexed":true},
    {"name":"sizeHint","type":"uint256","indexed":false},
    {"name":"action","type":"uint8","indexed":false},
    {"name":"stamper","type":"address","indexed":false},
    {"name":"stampedAt","type":"uint64","indexed":false},
    {"name":"note","type":"string","indexed":false}
  ]}
]`

// OnchainReceipt is PulseTradeStamp.getReceipt.
type OnchainReceipt struct {
	DecisionHash [32]byte
	SymbolID     *big.Int
	SizeHint     *big.Int
	Action       uint8
	Note         string
	StampedAt    uint64
	Stamper      common.Address
}

func contractABI() abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(contractABIJSON))
	if err != nil {
		panic(err)
	}
	return parsed
}

// WantLiveFromEnv is true when PULSE_STAMP_LIVE (or legacy PULSE_LIVE_STAMP) is set.
func WantLiveFromEnv() bool {
	return envTruthy("PULSE_STAMP_LIVE") || envTruthy("PULSE_LIVE_STAMP")
}

func envTruthy(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// ExplorerTxURL is the MonadVision testnet tx page.
func ExplorerTxURL(txHash string) string {
	h := strings.TrimSpace(txHash)
	if h == "" {
		return ""
	}
	if !strings.HasPrefix(h, "0x") && !strings.HasPrefix(h, "0X") {
		h = "0x" + h
	}
	return ExplorerTxBase + h
}

// StampedTopic0 is keccak256 of the Stamped event signature.
func StampedTopic0() common.Hash {
	return crypto.Keccak256Hash([]byte(StampedEvent))
}

// ParseStampedID reads the indexed receipt id from a Stamped log.
func ParseStampedID(logs []*types.Log) (*big.Int, error) {
	want := StampedTopic0()
	for _, lg := range logs {
		if lg == nil || len(lg.Topics) < 2 {
			continue
		}
		if lg.Topics[0] != want {
			continue
		}
		return new(big.Int).SetBytes(lg.Topics[1].Bytes()), nil
	}
	return nil, fmt.Errorf("stamp: Stamped event not found")
}

// EncodeGetReceipt ABI-encodes getReceipt(uint256).
func EncodeGetReceipt(id *big.Int) ([]byte, error) {
	if id == nil {
		return nil, fmt.Errorf("stamp: nil receipt id")
	}
	return contractABI().Pack("getReceipt", id)
}

func waitReceipt(ctx context.Context, client *ethclient.Client, hash common.Hash) (*types.Receipt, error) {
	deadline := time.Now().Add(45 * time.Second)
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	for {
		rec, err := client.TransactionReceipt(ctx, hash)
		if err == nil && rec != nil {
			return rec, nil
		}
		if err != nil && err != ethereum.NotFound {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("stamp: timed out waiting for %s", hash.Hex())
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

func callGetReceipt(ctx context.Context, client *ethclient.Client, to common.Address, id *big.Int) (*OnchainReceipt, error) {
	data, err := EncodeGetReceipt(id)
	if err != nil {
		return nil, err
	}
	raw, err := client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	return unpackReceipt(raw)
}

// receiptArgs is the Solidity Receipt tuple flattened. A function returning
// one struct encodes identically to returning its fields, and unpacking as
// seven arguments avoids go-ethereum's tuple→struct setArray panic.
func receiptArgs() abi.Arguments {
	must := func(typ string) abi.Type {
		t, err := abi.NewType(typ, "", nil)
		if err != nil {
			panic(err)
		}
		return t
	}
	return abi.Arguments{
		{Name: "decisionHash", Type: must("bytes32")},
		{Name: "symbolId", Type: must("uint256")},
		{Name: "sizeHint", Type: must("uint256")},
		{Name: "action", Type: must("uint8")},
		{Name: "note", Type: must("string")},
		{Name: "stampedAt", Type: must("uint64")},
		{Name: "stamper", Type: must("address")},
	}
}

func unpackReceipt(raw []byte) (rec *OnchainReceipt, err error) {
	defer func() {
		if r := recover(); r != nil {
			rec = nil
			err = fmt.Errorf("stamp: unpack getReceipt panic: %v", r)
		}
	}()
	if len(raw) < 32 {
		return nil, fmt.Errorf("stamp: empty getReceipt return")
	}

	// Official path: ABI knows Receipt is a dynamic tuple, so eth_call data
	// starts with an offset (0x20) then the 7-field body.
	if method, ok := contractABI().Methods["getReceipt"]; ok {
		if vals, e := method.Outputs.Unpack(raw); e == nil && len(vals) == 1 {
			if rec, e := receiptFromValue(vals[0]); e == nil {
				return rec, nil
			}
		}
	}

	// Fallback: unpack the 7 fields from the tuple body (with or without offset).
	body := raw
	if off, ok := leadingOffset(raw); ok {
		body = raw[off:]
	}
	vals, err := receiptArgs().Unpack(body)
	if err != nil {
		return nil, fmt.Errorf("stamp: unpack getReceipt: %w", err)
	}
	return receiptFromSlice(vals)
}

func leadingOffset(raw []byte) (int, bool) {
	if len(raw) < 32 {
		return 0, false
	}
	off := new(big.Int).SetBytes(raw[:32])
	if !off.IsUint64() {
		return 0, false
	}
	n := int(off.Uint64())
	if n >= 32 && n < len(raw) && n%32 == 0 {
		return n, true
	}
	return 0, false
}

func receiptFromSlice(vals []any) (*OnchainReceipt, error) {
	if len(vals) != 7 {
		return nil, fmt.Errorf("stamp: getReceipt got %d fields, want 7", len(vals))
	}
	hash, err := asBytes32(vals[0])
	if err != nil {
		return nil, err
	}
	symbolID, err := asBig(vals[1])
	if err != nil {
		return nil, err
	}
	sizeHint, err := asBig(vals[2])
	if err != nil {
		return nil, err
	}
	action, err := asUint8(vals[3])
	if err != nil {
		return nil, err
	}
	note, ok := vals[4].(string)
	if !ok {
		return nil, fmt.Errorf("stamp: note is %T", vals[4])
	}
	stampedAt, err := asUint64(vals[5])
	if err != nil {
		return nil, err
	}
	stamper, err := asAddress(vals[6])
	if err != nil {
		return nil, err
	}
	return &OnchainReceipt{
		DecisionHash: hash,
		SymbolID:     symbolID,
		SizeHint:     sizeHint,
		Action:       action,
		Note:         note,
		StampedAt:    stampedAt,
		Stamper:      stamper,
	}, nil
}

func receiptFromValue(v any) (*OnchainReceipt, error) {
	if sl, ok := v.([]any); ok {
		return receiptFromSlice(sl)
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, fmt.Errorf("stamp: nil getReceipt tuple")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("stamp: getReceipt tuple is %T", v)
	}
	byName := map[string]any{}
	for i := 0; i < rv.NumField(); i++ {
		byName[normName(rv.Type().Field(i).Name)] = rv.Field(i).Interface()
	}
	pick := func(names ...string) (any, bool) {
		for _, n := range names {
			if v, ok := byName[normName(n)]; ok {
				return v, true
			}
		}
		return nil, false
	}
	get := func(names ...string) (any, error) {
		v, ok := pick(names...)
		if !ok {
			return nil, fmt.Errorf("stamp: missing field %v", names)
		}
		return v, nil
	}
	h, err := get("decisionHash")
	if err != nil {
		return nil, err
	}
	sid, err := get("symbolId", "symbolID")
	if err != nil {
		return nil, err
	}
	sz, err := get("sizeHint")
	if err != nil {
		return nil, err
	}
	act, err := get("action")
	if err != nil {
		return nil, err
	}
	note, err := get("note")
	if err != nil {
		return nil, err
	}
	ts, err := get("stampedAt")
	if err != nil {
		return nil, err
	}
	st, err := get("stamper")
	if err != nil {
		return nil, err
	}
	return receiptFromSlice([]any{h, sid, sz, act, note, ts, st})
}

func normName(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}

func asBytes32(v any) ([32]byte, error) {
	switch t := v.(type) {
	case [32]byte:
		return t, nil
	case []byte:
		if len(t) != 32 {
			return [32]byte{}, fmt.Errorf("stamp: bytes32 len %d", len(t))
		}
		var out [32]byte
		copy(out[:], t)
		return out, nil
	default:
		return [32]byte{}, fmt.Errorf("stamp: bytes32 is %T", v)
	}
}

func asBig(v any) (*big.Int, error) {
	switch t := v.(type) {
	case *big.Int:
		return t, nil
	case big.Int:
		n := t
		return &n, nil
	default:
		return nil, fmt.Errorf("stamp: uint256 is %T", v)
	}
}

func asUint8(v any) (uint8, error) {
	switch t := v.(type) {
	case uint8:
		return t, nil
	case uint16:
		return uint8(t), nil
	case uint64:
		return uint8(t), nil
	case *big.Int:
		if t == nil || !t.IsUint64() || t.Uint64() > 255 {
			return 0, fmt.Errorf("stamp: action out of range")
		}
		return uint8(t.Uint64()), nil
	default:
		return 0, fmt.Errorf("stamp: uint8 is %T", v)
	}
}

func asUint64(v any) (uint64, error) {
	switch t := v.(type) {
	case uint64:
		return t, nil
	case uint32:
		return uint64(t), nil
	case *big.Int:
		if t == nil || !t.IsUint64() {
			return 0, fmt.Errorf("stamp: uint64 out of range")
		}
		return t.Uint64(), nil
	default:
		return 0, fmt.Errorf("stamp: uint64 is %T", v)
	}
}

func asAddress(v any) (common.Address, error) {
	switch t := v.(type) {
	case common.Address:
		return t, nil
	case [20]byte:
		return common.Address(t), nil
	default:
		return common.Address{}, fmt.Errorf("stamp: address is %T", v)
	}
}
