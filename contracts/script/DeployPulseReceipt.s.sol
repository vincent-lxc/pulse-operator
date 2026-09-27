// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Script, console2} from "forge-std/Script.sol";
import {PulseReceipt} from "../src/PulseReceipt.sol";

/// @notice Shared deploy script. Point `--rpc-url` at testnet or mainnet.
/// @dev Never put a private key in this file. Use env PRIVATE_KEY.
contract Deploy is Script {
    function run() external returns (PulseReceipt receipt) {
        uint256 expectedChainId = vm.envOr("EXPECTED_CHAIN_ID", uint256(0));
        if (expectedChainId != 0 && block.chainid != expectedChainId) {
            revert(string.concat("wrong chain id: got ", vm.toString(block.chainid)));
        }

        // Signer comes from env PRIVATE_KEY only — never hard-code a key here.
        uint256 deployerKey = vm.envUint("PRIVATE_KEY");
        address deployer = vm.addr(deployerKey);

        console2.log("Deployer:", deployer);
        console2.log("Chain id:", block.chainid);
        console2.log("Native USDC balance (18 decimals):", deployer.balance);

        vm.startBroadcast(deployerKey);
        receipt = new PulseReceipt();
        vm.stopBroadcast();

        console2.log("PulseReceipt:", address(receipt));
        _printExplorerHint(address(receipt));
    }

    function _printExplorerHint(address deployed) internal view {
        if (block.chainid == 5042) {
            console2.log("Explorer: https://explorer.arc.io/address/%s", deployed);
        } else if (block.chainid == 5042002) {
            console2.log("Explorer: https://explorer.testnet.arc.io/address/%s", deployed);
            console2.log("Arcscan:  https://testnet.arcscan.app/address/%s", deployed);
        }
    }
}
