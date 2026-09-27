// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {PulseTradeStamp} from "../src/PulseTradeStamp.sol";

contract PulseTradeStampTest is Test {
    PulseTradeStamp internal c;

    function setUp() public {
        c = new PulseTradeStamp();
    }

    function test_stamp_buy() public {
        bytes32 h = keccak256("decision-1");
        uint256 id = c.stamp(h, 1, 100, 1, "buy BTC");
        assertEq(id, 1);
        PulseTradeStamp.Receipt memory r = c.getReceipt(1);
        assertEq(r.decisionHash, h);
        assertEq(r.symbolId, 1);
        assertEq(r.sizeHint, 100);
        assertEq(r.action, 1);
        assertEq(r.stamper, address(this));
    }

    function test_revert_zero_hash() public {
        vm.expectRevert(PulseTradeStamp.ZeroHash.selector);
        c.stamp(bytes32(0), 1, 0, 0, "");
    }

    function test_revert_bad_action() public {
        vm.expectRevert(PulseTradeStamp.BadAction.selector);
        c.stamp(keccak256("x"), 1, 0, 3, "");
    }

    function test_revert_note_too_long() public {
        bytes memory b = new bytes(281);
        for (uint i; i < 281; i++) b[i] = "a";
        vm.expectRevert(PulseTradeStamp.NoteTooLong.selector);
        c.stamp(keccak256("x"), 1, 0, 0, string(b));
    }
}
