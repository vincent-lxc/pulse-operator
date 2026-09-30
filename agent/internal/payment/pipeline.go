package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/vincent-lxc/pulse-on-monad/agent/internal/runlock"
	"os"
	"path/filepath"
)

type journal struct {
	Intent Intent      `json:"intent"`
	Stage  string      `json:"stage"`
	TxHash common.Hash `json:"tx_hash"`
	Result *Result     `json:"result,omitempty"`
}

func DryRun(in Intent) (*Result, error) {
	data, err := Calldata(in)
	if err != nil {
		return nil, err
	}
	return &Result{Status: "dry_run", DecisionHash: in.DecisionHash, Calldata: hexutil.Encode(data)}, nil
}

// Execute persists invoice identity before submission, reconciles the chain on
// every restart, and never resends an uncertain transaction automatically.
func Execute(ctx context.Context, dir string, in Intent, backend Backend) (*Result, error) {
	unlock, err := runlock.Acquire(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	path := filepath.Join(dir, in.DecisionHash.Hex()+".json")
	j := journal{Intent: in, Stage: "prepared"}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &j); err != nil {
			return nil, fmt.Errorf("invalid payment journal: %w", err)
		}
		if j.Intent != in {
			return nil, fmt.Errorf("payment_id cannot change category, payee, amount or scope")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	recovered, err := backend.Recover(ctx, in)
	if err != nil {
		return nil, err
	}
	if recovered != nil {
		j.Stage = "complete"
		j.Result = recovered
		j.TxHash = recovered.TxHash
		if err := saveJournal(path, j); err != nil {
			return nil, err
		}
		return recovered, nil
	}
	if j.Stage == "complete" {
		return nil, fmt.Errorf("journal completed but chain has no decision; refusing replay on reset/replaced chain")
	}
	if j.Stage == "submitting" {
		return nil, fmt.Errorf("submission outcome unknown: keep payment_id and reconcile the original chain; automatic resend disabled")
	}
	if j.Stage == "submitted" {
		if j.TxHash == (common.Hash{}) {
			return nil, fmt.Errorf("submitted journal missing tx hash")
		}
	} else if j.Stage == "prepared" {
		j.Stage = "submitting"
		if err := saveJournal(path, j); err != nil {
			return nil, err
		}
		hash, err := backend.Send(ctx, in)
		if err != nil {
			return nil, err
		}
		j.TxHash = hash
		j.Stage = "submitted"
		if err := saveJournal(path, j); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("unknown journal stage %q", j.Stage)
	}
	result, err := backend.Wait(ctx, in, j.TxHash)
	if err != nil {
		return nil, err
	}
	j.Stage = "complete"
	j.Result = result
	if err := saveJournal(path, j); err != nil {
		return nil, err
	}
	return result, nil
}

// Sync then rename makes each journal revision durable and indivisible.
// https://pkg.go.dev/os#File.Sync and https://pkg.go.dev/os#Rename
func saveJournal(path string, j journal) error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".payment-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
