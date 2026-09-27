#!/usr/bin/env bash
# Reproduce the Arc testnet smoke test (docs/testnet-smoke.md) against a deployed PolicyVault.
# Uses `cast send` because Foundry's local EVM can't simulate Arc USDC transfers (precompile 0x1800…).
# Keys are read from env only (never hard-coded or committed).
#   OWNER_PRIVATE_KEY, AGENT_PRIVATE_KEY  (required)
#   VAULT_ADDRESS, PAYEE_ADDRESS, AGENT_ADDRESS (required)
#   ARC_TESTNET_RPC_URL (default https://rpc.testnet.arc.io)
set -euo pipefail
RPC=${ARC_TESTNET_RPC_URL:-https://rpc.testnet.arc.io}
USDC=0x3600000000000000000000000000000000000000
[ "$(cast chain-id --rpc-url "$RPC")" = "5042002" ] || { echo "Arc testnet only"; exit 1; }
GAS=(--gas-price 50gwei --priority-gas-price 2gwei) # Arc base fee floor is 20 gwei
CAT=$(cast format-bytes32-string api)
d() { cast keccak "{\"v\":1,\"actor\":\"$1\",\"action\":\"$2\",\"category\":\"api\",\"payee\":\"$PAYEE_ADDRESS\",\"amount_usdc\":\"$3\",\"note\":\"$4\"}"; }
send() { local key=$1; shift; cast send --rpc-url "$RPC" --private-key "$key" "${GAS[@]}" "$@" | grep -E "^(transactionHash|status|blockNumber)"; }

echo "1. deposit 5 USDC";        send "$OWNER_PRIVATE_KEY" $USDC 'transfer(address,uint256)' "$VAULT_ADDRESS" 5000000
echo "2. setCategory api";       send "$OWNER_PRIVATE_KEY" "$VAULT_ADDRESS" 'setCategory(bytes32,uint256,uint256,uint64,bytes32)' "$CAT" 2000000 1000000 604800 "$(d owner set_policy 0 policy)"
echo "3. allowlist payee";       send "$OWNER_PRIVATE_KEY" "$VAULT_ADDRESS" 'setPayee(bytes32,address,bool,bytes32)' "$CAT" "$PAYEE_ADDRESS" true "$(d owner set_payee 0 payee)"
echo "4. agent pays 0.5";        send "$AGENT_PRIVATE_KEY" "$VAULT_ADDRESS" 'pay(bytes32,address,uint256,bytes32)' "$CAT" "$PAYEE_ADDRESS" 500000 "$(d agent pay 0.5 "ok-$(date +%s)")"
echo "5. agent pays 1.5 (cap)";  send "$AGENT_PRIVATE_KEY" "$VAULT_ADDRESS" 'pay(bytes32,address,uint256,bytes32)' "$CAT" "$PAYEE_ADDRESS" 1500000 "$(d agent pay 1.5 "big-$(date +%s)")"
ID=$(cast call "$VAULT_ADDRESS" 'pendingRequestIds()(uint256[])' --rpc-url "$RPC" | tr -d '[] ' | awk -F, '{print $NF}')
echo "6. owner approves $ID";    send "$OWNER_PRIVATE_KEY" "$VAULT_ADDRESS" 'approve(uint256)' "$ID"
echo "7. non-allowlisted probe (eth_call, expect PayeeNotAllowed revert)"
cast call "$VAULT_ADDRESS" 'pay(bytes32,address,uint256,bytes32)(bool,uint256)' "$CAT" 0x000000000000000000000000000000000000dEaD 100000 "$(d agent pay 0.1 probe)" --from "$AGENT_ADDRESS" --rpc-url "$RPC" && { echo "UNEXPECTED: probe succeeded"; exit 1; } || echo "reverted as expected"
echo "8. sweep 0.5 to reserve";  send "$OWNER_PRIVATE_KEY" "$VAULT_ADDRESS" 'sweepToReserve(uint256,bytes32)' 500000 "$(d owner sweep 0.5 sweep)"
cast call "$VAULT_ADDRESS" 'counters()((uint256,uint256,uint256,uint256))' --rpc-url "$RPC"
