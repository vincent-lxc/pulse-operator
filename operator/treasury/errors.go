package treasury

import "errors"

// ErrOwnerKeyRequired 表示 sweepToReserve 需要 owner 密钥，而当前只有 agent 密钥。
var ErrOwnerKeyRequired = errors.New("sweep requires owner key")

// ErrAgentKeyRequired 表示 live pay 需要 operator 私钥。
var ErrAgentKeyRequired = errors.New("pay requires agent key")
