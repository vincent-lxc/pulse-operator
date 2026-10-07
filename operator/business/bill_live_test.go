package business

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func TestCommitBurnSavesBeforePoll(t *testing.T) {
	var order []string
	prev := pollBurnMessages
	pollBurnMessages = func(context.Context, treasury.Config, string) (procurement.Message, error) {
		order = append(order, "poll")
		return procurement.Message{}, fmt.Errorf("iris timeout")
	}
	t.Cleanup(func() { pollBurnMessages = prev })
	_, err := commitBurn(context.Background(), treasury.Config{}, "0xabc", "circle-1", big.NewInt(10), func(tx string) error {
		order = append(order, "save:"+tx)
		return nil
	})
	if err == nil || len(order) != 2 || order[0] != "save:0xabc" || order[1] != "poll" {
		t.Fatalf("order %v err %v", order, err)
	}
}
