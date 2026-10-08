// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {MainnetGuard} from "../src/MainnetGuard.sol";
import {SetupDomains} from "../script/SetupDomains.s.sol";

contract MainnetGuardTest is Test {
    function test_refusesPulseReceiptOnMainnet() public {
        vm.expectRevert(bytes("refusing PulseReceipt on Arc mainnet"));
        this.check(5042, 0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01);
    }

    function test_allowsANewMainnetVault() public view {
        this.check(5042, address(0x1234));
    }

    function test_allowsTheSameAddressOnTestnet() public view {
        this.check(5042002, 0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01);
    }

    function test_setupScriptCompiles() public {
        SetupDomains script = new SetupDomains();
        assertTrue(address(script) != address(0));
    }

    function check(uint256 chainId, address vault) external pure {
        MainnetGuard.assertVault(chainId, vault);
    }
}
