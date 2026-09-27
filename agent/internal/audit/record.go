package audit

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

// Action matches PulseTradeStamp: 0 hold / 1 buy / 2 sell.
const (
	ActionHold uint8 = 0
	ActionBuy  uint8 = 1
	ActionSell uint8 = 2
)

// Record is one append-only JSONL decision. ContentHash / DecisionHash are
// keccak256 of the canonical payload (this struct without those two fields).
type Record struct {
	RunID        string `json:"run_id"`
	AgentID      string `json:"agent_id"`
	TS           int64  `json:"ts"`
	Steps        []Step `json:"steps"`
	FinalAction  string `json:"final_action"`
	ActionCode   uint8  `json:"action_code"`
	SizeHint     uint64 `json:"size_hint"`
	SymbolID     uint64 `json:"symbol_id"`
	ContentHash  string `json:"content_hash,omitempty"`
	DecisionHash string `json:"decision_hash,omitempty"`
}

// Step is one pipeline stage (observe, kronos, plan, risk, jev, execute).
type Step struct {
	Name          string          `json:"name"`
	ModelID       string          `json:"model_id,omitempty"`
	ModelVersion  string          `json:"model_version,omitempty"`
	InputsHash    string          `json:"inputs_hash"`
	Outputs       json.RawMessage `json:"outputs"`
	Risk          *RiskIO         `json:"risk,omitempty"`
	Jev           *JevIO          `json:"jev,omitempty"`
}

// RiskIO is the hard-risk verdict attached to the risk step.
type RiskIO struct {
	Pass   bool   `json:"pass"`
	Reason string `json:"reason"`
}

// JevIO is optional soft-gate I/O. Never store an API key here.
type JevIO struct {
	Prompt    string          `json:"prompt,omitempty"`
	Verdict   string          `json:"verdict,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	ModelID   string          `json:"model_id,omitempty"`
	Questions json.RawMessage `json:"questions,omitempty"`
	Answers   json.RawMessage `json:"answers,omitempty"`
}

type canonicalRecord struct {
	RunID       string          `json:"run_id"`
	AgentID     string          `json:"agent_id"`
	TS          int64           `json:"ts"`
	Steps       []canonicalStep `json:"steps"`
	FinalAction string          `json:"final_action"`
	ActionCode  uint8           `json:"action_code"`
	SizeHint    uint64          `json:"size_hint"`
	SymbolID    uint64          `json:"symbol_id"`
}

type canonicalStep struct {
	Name         string          `json:"name"`
	ModelID      string          `json:"model_id,omitempty"`
	ModelVersion string          `json:"model_version,omitempty"`
	InputsHash   string          `json:"inputs_hash"`
	Outputs      json.RawMessage `json:"outputs"`
	Risk         *RiskIO         `json:"risk,omitempty"`
	Jev          *JevIO          `json:"jev,omitempty"`
}

// NewRecord builds a record at now (UTC seconds).
func NewRecord(runID, agentID string) Record {
	return Record{
		RunID:   runID,
		AgentID: agentID,
		TS:      time.Now().UTC().Unix(),
		Steps:   nil,
	}
}

// CanonicalBytes is the deterministic JSON hashed into decisionHash.
func (r Record) CanonicalBytes() ([]byte, error) {
	steps := make([]canonicalStep, 0, len(r.Steps))
	for _, s := range r.Steps {
		out := s.Outputs
		if len(out) == 0 {
			out = json.RawMessage(`{}`)
		}
		steps = append(steps, canonicalStep{
			Name:         s.Name,
			ModelID:      s.ModelID,
			ModelVersion: s.ModelVersion,
			InputsHash:   s.InputsHash,
			Outputs:      out,
			Risk:         s.Risk,
			Jev:          s.Jev,
		})
	}
	payload := canonicalRecord{
		RunID:       r.RunID,
		AgentID:     r.AgentID,
		TS:          r.TS,
		Steps:       steps,
		FinalAction: r.FinalAction,
		ActionCode:  r.ActionCode,
		SizeHint:    r.SizeHint,
		SymbolID:    r.SymbolID,
	}
	return marshalCanonical(payload)
}

// Seal writes ContentHash and DecisionHash (same keccak256 hex).
func (r *Record) Seal() error {
	sum, err := r.Hash()
	if err != nil {
		return err
	}
	hexHash := "0x" + hex.EncodeToString(sum[:])
	r.ContentHash = hexHash
	r.DecisionHash = hexHash
	return nil
}

// Hash returns keccak256(canonical JSON).
func (r Record) Hash() ([32]byte, error) {
	b, err := r.CanonicalBytes()
	if err != nil {
		return [32]byte{}, err
	}
	sum := crypto.Keccak256(b)
	var out [32]byte
	copy(out[:], sum)
	return out, nil
}

// HashHex is 0x-prefixed keccak of the canonical payload.
func (r Record) HashHex() (string, error) {
	sum, err := r.Hash()
	if err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(sum[:]), nil
}

// HashInputs keccak-hashes a JSON-encodable inputs object.
func HashInputs(v any) (string, error) {
	b, err := marshalCanonical(v)
	if err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(crypto.Keccak256(b)), nil
}

func marshalCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("canonical json: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// ParseAction maps planner language to the on-chain uint8.
func ParseAction(s string) uint8 {
	switch s {
	case "buy", "BUY":
		return ActionBuy
	case "sell", "SELL":
		return ActionSell
	default:
		return ActionHold
	}
}

// MustRaw is json.Marshal that panics only on programmer error (used for step outputs).
func MustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
