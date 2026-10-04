package treasury

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func TestDecodeRequestNotPendingAndDecisionAlreadyUsed(t *testing.T) {
	pending := packError(t, "RequestNotPending", big.NewInt(3))
	got, ok := DecodeRevertData(pending)
	if !ok || got != "RequestNotPending(3)" {
		t.Fatalf("pending decode %q ok=%v", got, ok)
	}
	var hash [32]byte
	hash[31] = 0xab
	used := packError(t, "DecisionAlreadyUsed", hash)
	got, ok = DecodeRevertData(used)
	if !ok || !strings.HasPrefix(got, "DecisionAlreadyUsed(0x") || !strings.HasSuffix(got, "ab)") {
		t.Fatalf("used decode %q ok=%v", got, ok)
	}
	err := AnnotateRevert(fmt.Errorf("execution reverted: 0x%x", pending))
	if RevertReason(err) != "RequestNotPending(3)" {
		t.Fatalf("annotated %v", err)
	}
	plain := AnnotateRevert(fmt.Errorf("dial tcp: connection refused"))
	if RevertReason(plain) != "" || plain.Error() != "dial tcp: connection refused" {
		t.Fatalf("plain error changed: %v", plain)
	}
}

func packError(t *testing.T, name string, args ...interface{}) []byte {
	t.Helper()
	item, ok := contractABI.Errors[name]
	if !ok {
		t.Fatalf("missing error %s", name)
	}
	packed, err := item.Inputs.Pack(args...)
	if err != nil {
		t.Fatal(err)
	}
	out := append([]byte{}, item.ID[:4]...)
	return append(out, packed...)
}
