// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {PulseReceipt} from "../src/PulseReceipt.sol";

contract PulseReceiptTest is Test {
    PulseReceipt internal receipt;

    bytes32 internal constant SAMPLE_HASH = keccak256("pulse-alert-btc-500");

    event ReceiptStamped(
        address indexed stamper,
        uint256 indexed cmcId,
        bytes32 alertHash,
        int256 threshold,
        uint8 kind,
        string note,
        uint256 stampedAt
    );

    function setUp() public {
        receipt = new PulseReceipt();
    }

    function test_stampReceipt_emitsAndStores() public {
        vm.expectEmit(true, true, false, true);
        emit ReceiptStamped(address(this), 1, SAMPLE_HASH, 500, 0, "BTC above threshold", block.timestamp);

        uint256 id = receipt.stampReceipt(1, SAMPLE_HASH, 500, 0, "BTC above threshold");

        assertEq(id, 1);
        assertEq(receipt.receiptCount(), 1);

        PulseReceipt.Receipt memory stored = receipt.getReceipt(1);
        assertEq(stored.stamper, address(this));
        assertEq(stored.cmcId, 1);
        assertEq(stored.alertHash, SAMPLE_HASH);
        assertEq(stored.threshold, 500);
        assertEq(stored.kind, receipt.KIND_ALERT());
        assertEq(stored.note, "BTC above threshold");
        assertEq(stored.stampedAt, block.timestamp);
    }

    function test_stampReceipt_manualKindAndEmptyNote() public {
        uint256 id = receipt.stampReceipt(1027, SAMPLE_HASH, -250, 1, "");
        PulseReceipt.Receipt memory stored = receipt.getReceipt(id);

        assertEq(stored.kind, receipt.KIND_MANUAL());
        assertEq(stored.threshold, -250);
        assertEq(stored.cmcId, 1027);
        assertEq(bytes(stored.note).length, 0);
    }

    function test_stampReceipt_incrementsIds() public {
        uint256 first = receipt.stampReceipt(1, keccak256("a"), 1, 0, "one");
        uint256 second = receipt.stampReceipt(2, keccak256("b"), 2, 1, "two");

        assertEq(first, 1);
        assertEq(second, 2);
        assertEq(receipt.receiptCount(), 2);
        assertEq(receipt.getReceipt(2).note, "two");
    }

    function test_stampReceipt_differentStampers() public {
        address alice = makeAddr("alice");
        address bob = makeAddr("bob");

        vm.prank(alice);
        receipt.stampReceipt(1, keccak256("alice"), 10, 0, "alice");

        vm.prank(bob);
        receipt.stampReceipt(1, keccak256("bob"), -10, 1, "bob");

        assertEq(receipt.getReceipt(1).stamper, alice);
        assertEq(receipt.getReceipt(2).stamper, bob);
    }

    function test_stampReceipt_acceptsMaxNote() public {
        string memory note = _repeat("x", 280);
        receipt.stampReceipt(1, SAMPLE_HASH, 0, 0, note);
        assertEq(bytes(receipt.getReceipt(1).note).length, 280);
    }

    function test_revert_emptyAlertHash() public {
        vm.expectRevert(PulseReceipt.EmptyAlertHash.selector);
        receipt.stampReceipt(1, bytes32(0), 500, 0, "no hash");
    }

    function test_revert_unknownKind() public {
        vm.expectRevert(PulseReceipt.UnknownKind.selector);
        receipt.stampReceipt(1, SAMPLE_HASH, 500, 2, "bad kind");
    }

    function test_revert_noteTooLong() public {
        string memory note = _repeat("n", 281);
        vm.expectRevert(PulseReceipt.NoteTooLong.selector);
        receipt.stampReceipt(1, SAMPLE_HASH, 500, 0, note);
    }

    function test_revert_getReceiptUnknown() public {
        vm.expectRevert(PulseReceipt.ReceiptNotFound.selector);
        receipt.getReceipt(0);

        vm.expectRevert(PulseReceipt.ReceiptNotFound.selector);
        receipt.getReceipt(1);
    }

    function testFuzz_stampReceipt(
        uint256 cmcId,
        bytes32 alertHash,
        int256 threshold,
        bool manual,
        bytes calldata rawNote
    ) public {
        vm.assume(alertHash != bytes32(0));
        string memory note = rawNote.length > 280 ? string(rawNote[:280]) : string(rawNote);
        uint8 kind = manual ? uint8(1) : uint8(0);

        uint256 id = receipt.stampReceipt(cmcId, alertHash, threshold, kind, note);
        PulseReceipt.Receipt memory stored = receipt.getReceipt(id);

        assertEq(id, 1);
        assertEq(stored.cmcId, cmcId);
        assertEq(stored.alertHash, alertHash);
        assertEq(stored.threshold, threshold);
        assertEq(stored.kind, kind);
        assertEq(stored.note, note);
        assertEq(stored.stamper, address(this));
    }

    function _repeat(bytes1 ch, uint256 n) internal pure returns (string memory) {
        bytes memory buf = new bytes(n);
        for (uint256 i = 0; i < n; i++) {
            buf[i] = ch;
        }
        return string(buf);
    }
}
