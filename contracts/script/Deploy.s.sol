// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Script, console2} from "forge-std/Script.sol";
import {PolicyVault} from "../src/PolicyVault.sol";

/// @notice Deploy PolicyVault to Arc. Signer comes from env PRIVATE_KEY only — never hard-code a key.
/// @dev Env:
///      PRIVATE_KEY        (required) deployer key
///      AGENT_ADDRESS      (required) operator key the AI agent signs `pay` with (must differ from owner)
///      OWNER_ADDRESS      (optional) human owner, default = deployer
///      RESERVE_ADDRESS    (optional) sweep destination, default = owner
///      USDC_ADDRESS       (optional) default = Arc native USDC ERC-20 0x3600…0000 (6 decimals)
///      EXPECTED_CHAIN_ID  (optional) abort if the RPC is on a different chain
///      CONFIRM_MAINNET=1  (required to broadcast on Arc mainnet, chainId 5042)
///      SEED_DEMO_POLICY=1 (optional, only if owner == deployer) create demo categories
contract DeployPolicyVault is Script {
    address internal constant ARC_USDC = 0x3600000000000000000000000000000000000000;
    uint256 internal constant ARC_MAINNET = 5042;
    uint256 internal constant ARC_TESTNET = 5042002;

    function run() external returns (PolicyVault vault) {
        uint256 expectedChainId = vm.envOr("EXPECTED_CHAIN_ID", uint256(0));
        if (expectedChainId != 0 && block.chainid != expectedChainId) {
            revert(string.concat("wrong chain id: got ", vm.toString(block.chainid)));
        }
        if (block.chainid == ARC_MAINNET && vm.envOr("CONFIRM_MAINNET", uint256(0)) != 1) {
            revert("refusing Arc mainnet without CONFIRM_MAINNET=1");
        }

        uint256 deployerKey = vm.envUint("PRIVATE_KEY");
        address deployer = vm.addr(deployerKey);
        address owner = vm.envOr("OWNER_ADDRESS", deployer);
        address agent = vm.envAddress("AGENT_ADDRESS");
        address reserve = vm.envOr("RESERVE_ADDRESS", owner);
        address usdc = vm.envOr("USDC_ADDRESS", ARC_USDC);
        require(agent != owner, "agent key must differ from owner");

        console2.log("Chain id:", block.chainid);
        console2.log("Deployer:", deployer);
        console2.log("Owner:   ", owner);
        console2.log("Agent:   ", agent);
        console2.log("Reserve: ", reserve);
        console2.log("Token:   ", usdc);
        console2.log("Native USDC balance (18 decimals):", deployer.balance);
        _checkDecimals(usdc);

        vm.startBroadcast(deployerKey);
        vault = new PolicyVault(usdc, owner, agent, reserve);
        if (vm.envOr("SEED_DEMO_POLICY", uint256(0)) == 1 && owner == deployer) {
            // 6-decimal USDC amounts
            vault.setCategory("infra", 50e6, 10e6, 7 days, keccak256("seed:infra"));
            vault.setCategory("data", 20e6, 5e6, 7 days, keccak256("seed:data"));
        }
        vm.stopBroadcast();

        console2.log("PolicyVault:", address(vault));
        if (block.chainid == ARC_MAINNET) {
            console2.log("Explorer: https://explorer.arc.io/address/%s", address(vault));
        } else if (block.chainid == ARC_TESTNET) {
            console2.log("Explorer: https://explorer.testnet.arc.io/address/%s", address(vault));
        }
    }

    function _checkDecimals(address token) internal view {
        if (token.code.length == 0) {
            console2.log("WARNING: token has no code on this chain");
            return;
        }
        (bool ok, bytes memory ret) = token.staticcall(abi.encodeWithSignature("decimals()"));
        if (ok && ret.length >= 32) {
            uint256 d = abi.decode(ret, (uint256));
            console2.log("Token decimals:", d);
            require(
                block.chainid != ARC_MAINNET && block.chainid != ARC_TESTNET || d == 6,
                "Arc USDC ERC-20 must be 6 decimals"
            );
        }
    }
}
