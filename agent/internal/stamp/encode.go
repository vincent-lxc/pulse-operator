package stamp

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// PulseTradeStamp.stamp(bytes32,uint256,uint256,uint8,string)
const StampSignature = "stamp(bytes32,uint256,uint256,uint8,string)"

const stampABIJSON = `[{"type":"function","name":"stamp","stateMutability":"nonpayable","inputs":[{"name":"decisionHash","type":"bytes32"},{"name":"symbolId","type":"uint256"},{"name":"sizeHint","type":"uint256"},{"name":"action","type":"uint8"},{"name":"note","type":"string"}],"outputs":[{"name":"id","type":"uint256"}]}]`

// Request is the five-arg stamp call.
type Request struct {
	DecisionHash [32]byte
	SymbolID     *big.Int
	SizeHint     *big.Int
	Action       uint8
	Note         string
}

func parsedABI() abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(stampABIJSON))
	if err != nil {
		panic(err)
	}
	return parsed
}

// Selector is keccak256("stamp(bytes32,uint256,uint256,uint8,string)")[:4].
func Selector() []byte {
	return crypto.Keccak256([]byte(StampSignature))[:4]
}

// EncodeCalldata ABI-encodes stamp(...). Note must be ≤ 280 bytes (contract).
func EncodeCalldata(req Request) ([]byte, error) {
	if req.DecisionHash == ([32]byte{}) {
		return nil, fmt.Errorf("stamp: zero decision hash")
	}
	if req.Action > 2 {
		return nil, fmt.Errorf("stamp: bad action %d", req.Action)
	}
	if len(req.Note) > 280 {
		return nil, fmt.Errorf("stamp: note too long (%d)", len(req.Note))
	}
	if req.SymbolID == nil {
		req.SymbolID = big.NewInt(0)
	}
	if req.SizeHint == nil {
		req.SizeHint = big.NewInt(0)
	}
	data, err := parsedABI().Pack("stamp", req.DecisionHash, req.SymbolID, req.SizeHint, req.Action, req.Note)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// NoteForAgent builds a short note that includes agentId (and run_id).
func NoteForAgent(agentID, runID string) string {
	n := fmt.Sprintf("agent=%s run=%s", agentID, runID)
	if len(n) > 280 {
		n = n[:280]
	}
	return n
}

func Hex(data []byte) string {
	return "0x" + common.Bytes2Hex(data)
}
