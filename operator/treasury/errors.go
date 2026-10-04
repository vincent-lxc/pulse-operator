package treasury

import "errors"

// ErrOwnerKeyRequired 表示 sweep、approve、reject 需要 owner 签名者。
var ErrOwnerKeyRequired = errors.New("owner signer is required")

// ErrAgentKeyRequired 表示 live pay 需要 operator 私钥。
var ErrAgentKeyRequired = errors.New("pay requires agent key")
