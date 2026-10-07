// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Script, console2} from "forge-std/Script.sol";
import {PolicyVault} from "../src/PolicyVault.sol";
import {MainnetGuard} from "../src/MainnetGuard.sol";

/// @notice Owner setup for the domains category. Does not deploy a vault.
/// @dev Env:
///      PRIVATE_KEY            owner key
///      VAULT_ADDRESS          PolicyVault
///      PROCUREMENT_ADDRESS    the only domains payee
///      AGENT_ADDRESS          optional, set when it differs from the current agent
///      EXPECTED_CHAIN_ID      abort on a different chain
///      CONFIRM_MAINNET=1      required on chain id 5042
///      DOMAINS_BUDGET         default 30_000_000 (30 USDC)
///      DOMAINS_PER_TX_CAP     default 15_000_000 (15 USDC)
///      DOMAINS_PERIOD         default 14 days
contract SetupDomains is Script {
    function run() external {
        uint256 expected = vm.envOr("EXPECTED_CHAIN_ID", uint256(0));
        if (expected != 0 && block.chainid != expected) {
            revert(string.concat("wrong chain id: got ", vm.toString(block.chainid)));
        }
        if (block.chainid == 5042 && vm.envOr("CONFIRM_MAINNET", uint256(0)) != 1) {
            revert("refusing Arc mainnet without CONFIRM_MAINNET=1");
        }
        address vaultAddr = vm.envAddress("VAULT_ADDRESS");
        MainnetGuard.assertVault(block.chainid, vaultAddr);
        address procurement = vm.envAddress("PROCUREMENT_ADDRESS");
        uint256 budget = vm.envOr("DOMAINS_BUDGET", uint256(30e6));
        uint256 perTx = vm.envOr("DOMAINS_PER_TX_CAP", uint256(15e6));
        uint256 period = vm.envOr("DOMAINS_PERIOD", uint256(14 days));

        PolicyVault vault = PolicyVault(vaultAddr);
        // pendingCount() reverts when the address is not a PolicyVault.
        console2.log("pending", vault.pendingCount());
        console2.log("procurement", procurement);

        uint256 key = vm.envUint("PRIVATE_KEY");
        vm.startBroadcast(key);
        vault.setCategory("domains", budget, perTx, uint64(period), keccak256("setup:domains"));
        vault.setPayee("domains", procurement, true, keccak256("setup:domains-payee"));
        vault.setOverLimitMode(PolicyVault.OverLimitMode.Escalate, keccak256("setup:domains-mode"));
        address agent = vm.envOr("AGENT_ADDRESS", address(0));
        if (agent != address(0) && agent != vault.agent()) {
            vault.setAgent(agent, keccak256("setup:domains-agent"));
        }
        vm.stopBroadcast();
    }
}
