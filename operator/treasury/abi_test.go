package treasury

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestPackPaySelector(t *testing.T) {
	data, err := PackPay("infra", "0x1111111111111111111111111111111111111111", big.NewInt(2_000_000), common.HexToHash("0x01"))
	if err != nil {
		t.Fatal(err)
	}
	sig := crypto.Keccak256([]byte("pay(bytes32,address,uint256,bytes32)"))[:4]
	if string(data[:4]) != string(sig) {
		t.Fatalf("selector %x", data[:4])
	}
	if !strings.Contains(common.Bytes2Hex(data), "1111111111111111111111111111111111111111") {
		t.Fatalf("payee missing in %x", data)
	}
}

func TestUnpackCategoryView(t *testing.T) {
	method := contractABI.Methods["getCategory"]
	type view struct {
		Enabled       bool
		Budget        *big.Int
		PerTxCap      *big.Int
		Period        uint64
		Epoch         uint64
		EpochStart    uint64
		EpochEnd      uint64
		Spent         *big.Int
		AutoSpent     *big.Int
		ApprovedSpent *big.Int
		Remaining     *big.Int
	}
	encoded, err := method.Outputs.Pack(view{
		Enabled: true, Budget: big.NewInt(20_000_000), PerTxCap: big.NewInt(5_000_000),
		Period: 604800, Epoch: 1, EpochStart: 2, EpochEnd: 3,
		Spent: big.NewInt(2_000_000), AutoSpent: big.NewInt(2_000_000), ApprovedSpent: big.NewInt(0),
		Remaining: big.NewInt(18_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	values, err := contractABI.Unpack("getCategory", encoded)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := categoryFromABI("infra", values[0])
	if err != nil {
		t.Fatal(err)
	}
	if !cat.Enabled || cat.Remaining.Cmp(big.NewInt(18_000_000)) != 0 || cat.PeriodSeconds != 604800 {
		t.Fatalf("%+v", cat)
	}
}

func TestPackSweepSelector(t *testing.T) {
	data, err := PackSweep(big.NewInt(4_400_000), common.HexToHash("0x02"))
	if err != nil {
		t.Fatal(err)
	}
	sig := crypto.Keccak256([]byte("sweepToReserve(uint256,bytes32)"))[:4]
	if string(data[:4]) != string(sig) {
		t.Fatalf("selector %x", data[:4])
	}
}
