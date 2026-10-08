// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @notice Shared check: the Arc testnet PolicyVault address is PulseReceipt on Arc mainnet.
library MainnetGuard {
    address internal constant PULSE_RECEIPT_ON_MAINNET = 0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01;
    uint256 internal constant ARC_MAINNET = 5042;

    function assertVault(uint256 chainId, address vault) internal pure {
        if (chainId == ARC_MAINNET && vault == PULSE_RECEIPT_ON_MAINNET) {
            revert("refusing PulseReceipt on Arc mainnet");
        }
    }
}
