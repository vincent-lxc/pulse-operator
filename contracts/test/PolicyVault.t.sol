// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {PolicyVault} from "../src/PolicyVault.sol";
import {MockERC20, FalseReturnERC20} from "./mocks/MockERC20.sol";

contract PolicyVaultTest is Test {
    PolicyVault internal vault;
    MockERC20 internal usdc;

    address internal owner = makeAddr("owner");
    address internal agent = makeAddr("agent");
    address internal reserve = makeAddr("reserve");
    address internal vendor = makeAddr("vendor");
    address internal stranger = makeAddr("stranger");

    bytes32 internal constant OPS = "ops";
    bytes32 internal constant ADS = "ads";
    uint256 internal constant USDC = 1e6;
    uint64 internal constant WEEK = 7 days;

    uint256 internal hashNonce;

    event AgentPaid(
        bytes32 indexed category, address indexed payee, uint256 amount, uint64 epoch, bytes32 indexed decisionHash
    );
    event ApprovalRequested(
        uint256 indexed requestId,
        bytes32 indexed category,
        address indexed payee,
        uint256 amount,
        uint8 reason,
        bytes32 decisionHash
    );
    event RequestApproved(
        uint256 indexed requestId,
        bytes32 indexed category,
        address indexed payee,
        uint256 amount,
        uint64 epoch,
        bytes32 decisionHash
    );
    event RequestRejected(
        uint256 indexed requestId, bytes32 indexed category, address indexed payee, uint256 amount, bytes32 decisionHash
    );
    event SweptToReserve(address indexed reserve, uint256 amount, bytes32 decisionHash);
    event Paused(address indexed by, bytes32 decisionHash);

    function setUp() public {
        vm.warp(1_800_000_000);
        usdc = new MockERC20();
        vault = new PolicyVault(address(usdc), owner, agent, reserve);
        usdc.mint(address(vault), 10_000 * USDC);

        vm.startPrank(owner);
        vault.setCategory(OPS, 500 * USDC, 100 * USDC, WEEK, keccak256("policy-v1"));
        vault.setPayee(OPS, vendor, true, bytes32(0));
        vm.stopPrank();
    }

    function _h() internal returns (bytes32) {
        return keccak256(abi.encode("decision", ++hashNonce));
    }

    function _pay(uint256 amount) internal returns (bool paid, uint256 id) {
        vm.prank(agent);
        return vault.pay(OPS, vendor, amount, _h());
    }

    // ------------------------------------------------------------ constructor / roles

    function test_constructor_setsRoles() public view {
        assertEq(address(vault.token()), address(usdc));
        assertEq(vault.owner(), owner);
        assertEq(vault.agent(), agent);
        assertEq(vault.reserve(), reserve);
        assertFalse(vault.paused());
        assertEq(uint8(vault.overLimitMode()), uint8(PolicyVault.OverLimitMode.Escalate));
    }

    function test_constructor_revertsOnZero() public {
        vm.expectRevert(PolicyVault.ZeroAddress.selector);
        new PolicyVault(address(0), owner, agent, reserve);
        vm.expectRevert(PolicyVault.ZeroAddress.selector);
        new PolicyVault(address(usdc), owner, address(0), reserve);
    }

    function test_onlyAgentCanPay() public {
        vm.prank(owner);
        vm.expectRevert(PolicyVault.NotAgent.selector);
        vault.pay(OPS, vendor, 1 * USDC, _h());
    }

    function test_onlyOwnerAdmin() public {
        vm.startPrank(agent);
        vm.expectRevert(PolicyVault.NotOwner.selector);
        vault.setCategory(OPS, 1, 1, WEEK, bytes32(0));
        vm.expectRevert(PolicyVault.NotOwner.selector);
        vault.setPayee(OPS, agent, true, bytes32(0));
        vm.expectRevert(PolicyVault.NotOwner.selector);
        vault.pause();
        vm.expectRevert(PolicyVault.NotOwner.selector);
        vault.sweepToReserve(1);
        vm.expectRevert(PolicyVault.NotOwner.selector);
        vault.approve(1);
        vm.expectRevert(PolicyVault.NotOwner.selector);
        vault.reject(1);
        vm.expectRevert(PolicyVault.NotOwner.selector);
        vault.setAgent(agent, bytes32(0));
        vm.stopPrank();
    }

    // ------------------------------------------------------------ happy path

    function test_pay_withinLimits_transfers() public {
        bytes32 h = keccak256("buy-hosting");
        vm.expectEmit(true, true, true, true, address(vault));
        emit AgentPaid(OPS, vendor, 60 * USDC, 0, h);
        vm.prank(agent);
        (bool paid, uint256 id) = vault.pay(OPS, vendor, 60 * USDC, h);

        assertTrue(paid);
        assertEq(id, 0);
        assertEq(usdc.balanceOf(vendor), 60 * USDC);
        assertEq(vault.remainingBudget(OPS), 440 * USDC);
        assertTrue(vault.decisionUsed(h));
        assertEq(vault.counters().decidedByAgent, 1);
        PolicyVault.CategoryView memory v = vault.getCategory(OPS);
        assertEq(v.autoSpent, 60 * USDC);
        assertEq(v.approvedSpent, 0);
    }

    function test_pay_exactlyAtCapAndBudget() public {
        for (uint256 i; i < 5; ++i) {
            (bool paid,) = _pay(100 * USDC);
            assertTrue(paid);
        }
        assertEq(vault.remainingBudget(OPS), 0);
        (bool paid2, uint256 id) = _pay(1);
        assertFalse(paid2);
        assertEq(id, 1);
    }

    // ------------------------------------------------------------ rejections

    function test_pay_revertsNonAllowlistedPayee() public {
        vm.prank(agent);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.PayeeNotAllowed.selector, OPS, stranger));
        vault.pay(OPS, stranger, 1 * USDC, _h());
    }

    function test_pay_allowlistIsPerCategory() public {
        vm.prank(owner);
        vault.setCategory(ADS, 500 * USDC, 100 * USDC, WEEK, bytes32(0));
        vm.prank(agent);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.PayeeNotAllowed.selector, ADS, vendor));
        vault.pay(ADS, vendor, 1 * USDC, _h());
    }

    function test_pay_revertsUnknownCategory() public {
        vm.prank(agent);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.UnknownCategory.selector, ADS));
        vault.pay(ADS, vendor, 1 * USDC, _h());
    }

    function test_pay_revertsDisabledCategory() public {
        vm.prank(owner);
        vault.disableCategory(OPS, bytes32(0));
        vm.prank(agent);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.UnknownCategory.selector, OPS));
        vault.pay(OPS, vendor, 1 * USDC, _h());
        assertEq(vault.remainingBudget(OPS), 0);
    }

    function test_pay_revertsZeroHashAndZeroAmount() public {
        vm.startPrank(agent);
        vm.expectRevert(PolicyVault.ZeroDecisionHash.selector);
        vault.pay(OPS, vendor, 1, bytes32(0));
        vm.expectRevert(PolicyVault.ZeroAmount.selector);
        vault.pay(OPS, vendor, 0, keccak256("x"));
        vm.stopPrank();
    }

    function test_pay_decisionHashIsSingleUse() public {
        bytes32 h = keccak256("same-decision");
        vm.startPrank(agent);
        vault.pay(OPS, vendor, 1 * USDC, h);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.DecisionAlreadyUsed.selector, h));
        vault.pay(OPS, vendor, 1 * USDC, h);
        vm.stopPrank();
        assertEq(usdc.balanceOf(vendor), 1 * USDC);
    }

    function test_pay_revertsWhenVaultUnderfunded() public {
        vm.prank(owner);
        vault.sweepToReserve(10_000 * USDC - 10 * USDC);
        vm.prank(agent);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.InsufficientBalance.selector, 10 * USDC, 50 * USDC));
        vault.pay(OPS, vendor, 50 * USDC, _h());
    }

    function test_pay_revertsWhenTokenReturnsFalse() public {
        FalseReturnERC20 bad = new FalseReturnERC20();
        PolicyVault v2 = new PolicyVault(address(bad), owner, agent, reserve);
        bad.mint(address(v2), 100 * USDC);
        vm.startPrank(owner);
        v2.setCategory(OPS, 100 * USDC, 100 * USDC, WEEK, bytes32(0));
        v2.setPayee(OPS, vendor, true, bytes32(0));
        vm.stopPrank();
        vm.prank(agent);
        vm.expectRevert(PolicyVault.TransferFailed.selector);
        v2.pay(OPS, vendor, 1 * USDC, _h());
    }

    // ------------------------------------------------------------ escalation

    function test_overTxCap_escalates_noTransfer() public {
        bytes32 h = keccak256("big-invoice");
        vm.expectEmit(true, true, true, true, address(vault));
        emit ApprovalRequested(1, OPS, vendor, 150 * USDC, vault.REASON_OVER_TX_CAP(), h);
        vm.prank(agent);
        (bool paid, uint256 id) = vault.pay(OPS, vendor, 150 * USDC, h);

        assertFalse(paid);
        assertEq(id, 1);
        assertEq(usdc.balanceOf(vendor), 0);
        assertEq(vault.remainingBudget(OPS), 500 * USDC);
        assertEq(vault.pendingCount(), 1);
        PolicyVault.ApprovalRequest memory r = vault.getRequest(1);
        assertEq(r.amount, 150 * USDC);
        assertEq(r.decisionHash, h);
        assertEq(uint8(r.status), uint8(PolicyVault.RequestStatus.Pending));
        assertEq(r.reason, vault.REASON_OVER_TX_CAP());
        assertEq(vault.counters().escalated, 1);
        assertTrue(vault.decisionUsed(h));
    }

    function test_overBudget_escalates() public {
        for (uint256 i; i < 4; ++i) {
            _pay(100 * USDC);
        }
        (bool paid, uint256 id) = _pay(100 * USDC + 1); // over cap takes precedence
        assertEq(vault.getRequest(id).reason, vault.REASON_OVER_TX_CAP());
        (paid, id) = _pay(100 * USDC); // exactly fills budget
        assertTrue(paid);
        (paid, id) = _pay(1 * USDC);
        assertFalse(paid);
        assertEq(vault.getRequest(id).reason, vault.REASON_OVER_BUDGET());
        assertEq(usdc.balanceOf(vendor), 500 * USDC);
    }

    function test_revertMode_revertsInsteadOfEscalating() public {
        vm.prank(owner);
        vault.setOverLimitMode(PolicyVault.OverLimitMode.Revert, bytes32(0));
        bytes32 h = _h();
        uint8 reason = vault.REASON_OVER_TX_CAP();
        vm.prank(agent);
        vm.expectRevert(
            abi.encodeWithSelector(PolicyVault.OverLimit.selector, reason, 150 * USDC, 100 * USDC, 500 * USDC)
        );
        vault.pay(OPS, vendor, 150 * USDC, h);
        assertEq(vault.pendingCount(), 0);
        assertFalse(vault.decisionUsed(h)); // reverted, so hash can be retried after a policy change
    }

    function test_approve_transfersAndCountsAgainstBudget() public {
        (, uint256 id) = _pay(150 * USDC);
        bytes32 h = vault.getRequest(id).decisionHash;

        vm.expectEmit(true, true, true, true, address(vault));
        emit RequestApproved(id, OPS, vendor, 150 * USDC, 0, h);
        vm.prank(owner);
        vault.approve(id);

        assertEq(usdc.balanceOf(vendor), 150 * USDC);
        assertEq(vault.remainingBudget(OPS), 350 * USDC);
        assertEq(vault.pendingCount(), 0);
        assertEq(uint8(vault.getRequest(id).status), uint8(PolicyVault.RequestStatus.Approved));
        PolicyVault.CategoryView memory v = vault.getCategory(OPS);
        assertEq(v.approvedSpent, 150 * USDC);
        assertEq(v.autoSpent, 0);
        PolicyVault.Counters memory c = vault.counters();
        assertEq(c.escalated, 1);
        assertEq(c.approved, 1);
    }

    function test_approve_canExceedBudget_ownerOverride() public {
        for (uint256 i; i < 5; ++i) {
            _pay(100 * USDC);
        }
        (, uint256 id) = _pay(80 * USDC);
        vm.prank(owner);
        vault.approve(id);
        PolicyVault.CategoryView memory v = vault.getCategory(OPS);
        assertEq(v.spent, 580 * USDC);
        assertEq(v.autoSpent, 500 * USDC);
        assertEq(v.remaining, 0);
        // agent is still blocked
        (bool paid,) = _pay(1);
        assertFalse(paid);
    }

    function test_reject_noTransfer() public {
        (, uint256 id) = _pay(150 * USDC);
        bytes32 h = vault.getRequest(id).decisionHash;
        vm.expectEmit(true, true, true, true, address(vault));
        emit RequestRejected(id, OPS, vendor, 150 * USDC, h);
        vm.prank(owner);
        vault.reject(id);
        assertEq(usdc.balanceOf(vendor), 0);
        assertEq(vault.pendingCount(), 0);
        assertEq(vault.counters().rejected, 1);
        vm.startPrank(owner);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.RequestNotPending.selector, id));
        vault.approve(id);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.RequestNotPending.selector, id));
        vault.reject(id);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.RequestNotPending.selector, 99));
        vault.approve(99);
        vm.stopPrank();
    }

    function test_pendingList_swapAndPop() public {
        (, uint256 a) = _pay(101 * USDC);
        (, uint256 b) = _pay(102 * USDC);
        (, uint256 c) = _pay(103 * USDC);
        vm.prank(owner);
        vault.reject(a);
        uint256[] memory ids = vault.pendingRequestIds();
        assertEq(ids.length, 2);
        assertEq(ids[0], c);
        assertEq(ids[1], b);
        vm.prank(owner);
        vault.approve(b);
        ids = vault.pendingRequestIds();
        assertEq(ids.length, 1);
        assertEq(ids[0], c);
    }

    // ------------------------------------------------------------ epochs

    function test_epochRollover_resetsBudget() public {
        for (uint256 i; i < 5; ++i) {
            _pay(100 * USDC);
        }
        assertEq(vault.remainingBudget(OPS), 0);
        vm.warp(block.timestamp + WEEK);
        assertEq(vault.remainingBudget(OPS), 500 * USDC);
        PolicyVault.CategoryView memory v = vault.getCategory(OPS);
        assertEq(v.epoch, 1);
        assertEq(v.spent, 0);
        (bool paid,) = _pay(100 * USDC);
        assertTrue(paid);
        assertEq(vault.remainingBudget(OPS), 400 * USDC);
    }

    function test_setCategory_budgetChangeKeepsSpend_periodChangeResets() public {
        _pay(100 * USDC);
        vm.prank(owner);
        vault.setCategory(OPS, 300 * USDC, 100 * USDC, WEEK, bytes32(0));
        assertEq(vault.remainingBudget(OPS), 200 * USDC);
        vm.prank(owner);
        vault.setCategory(OPS, 300 * USDC, 100 * USDC, 30 days, bytes32(0));
        assertEq(vault.remainingBudget(OPS), 300 * USDC);
        vm.prank(owner);
        vm.expectRevert(PolicyVault.ZeroPeriod.selector);
        vault.setCategory(OPS, 1, 1, 0, bytes32(0));
    }

    // ------------------------------------------------------------ pause / sweep

    function test_pause_failsClosed() public {
        (, uint256 id) = _pay(150 * USDC);
        vm.expectEmit(true, true, true, true, address(vault));
        emit Paused(owner, keccak256("incident-42"));
        vm.prank(owner);
        vault.pause(keccak256("incident-42"));

        vm.prank(agent);
        vm.expectRevert(PolicyVault.IsPaused.selector);
        vault.pay(OPS, vendor, 1 * USDC, _h());

        vm.prank(owner);
        vm.expectRevert(PolicyVault.IsPaused.selector);
        vault.approve(id);

        // rejecting and sweeping are the safe direction and stay available
        vm.startPrank(owner);
        vault.reject(id);
        vault.sweepToReserve(1 * USDC);
        vault.unpause();
        vm.stopPrank();

        (bool paid,) = _pay(1 * USDC);
        assertTrue(paid);
    }

    function test_sweepToReserve() public {
        vm.expectEmit(true, true, true, true, address(vault));
        emit SweptToReserve(reserve, 1_000 * USDC, bytes32(0));
        vm.prank(owner);
        vault.sweepToReserve(1_000 * USDC);
        assertEq(usdc.balanceOf(reserve), 1_000 * USDC);
        assertEq(vault.balance(), 9_000 * USDC);

        address cold = makeAddr("cold");
        vm.startPrank(owner);
        vault.setReserve(cold, keccak256("rotate-reserve"));
        vault.sweepToReserve(500 * USDC, keccak256("weekly-sweep"));
        vm.expectRevert(PolicyVault.ZeroAmount.selector);
        vault.sweepToReserve(0);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.InsufficientBalance.selector, 8_500 * USDC, 9_000 * USDC));
        vault.sweepToReserve(9_000 * USDC);
        vm.stopPrank();
        assertEq(usdc.balanceOf(cold), 500 * USDC);
    }

    // ------------------------------------------------------------ admin

    function test_setAgent_rotatesKey() public {
        address agent2 = makeAddr("agent2");
        vm.prank(owner);
        vault.setAgent(agent2, bytes32(0));
        vm.prank(agent);
        vm.expectRevert(PolicyVault.NotAgent.selector);
        vault.pay(OPS, vendor, 1 * USDC, _h());
        vm.prank(agent2);
        (bool paid,) = vault.pay(OPS, vendor, 1 * USDC, _h());
        assertTrue(paid);
    }

    function test_ownership_twoStep() public {
        address newOwner = makeAddr("newOwner");
        vm.prank(owner);
        vault.transferOwnership(newOwner, keccak256("handover"));
        assertEq(vault.owner(), owner);
        vm.prank(stranger);
        vm.expectRevert(PolicyVault.NotPendingOwner.selector);
        vault.acceptOwnership();
        vm.prank(newOwner);
        vault.acceptOwnership();
        assertEq(vault.owner(), newOwner);
        assertEq(vault.pendingOwner(), address(0));
        vm.prank(owner);
        vm.expectRevert(PolicyVault.NotOwner.selector);
        vault.pause();
    }

    function test_payeeRemoval() public {
        vm.prank(owner);
        vault.setPayee(OPS, vendor, false, bytes32(0));
        vm.prank(agent);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.PayeeNotAllowed.selector, OPS, vendor));
        vault.pay(OPS, vendor, 1 * USDC, _h());
    }

    // ------------------------------------------------------------ fuzz

    /// @notice Any amount: either paid within both limits, or escalated with no transfer.
    function testFuzz_pay_paidIffWithinLimits(uint256 amount) public {
        amount = bound(amount, 1, 2_000 * USDC);
        uint256 before = usdc.balanceOf(vendor);
        (bool paid, uint256 id) = _pay(amount);
        bool within = amount <= 100 * USDC; // budget 500 > cap 100 on a fresh epoch
        assertEq(paid, within);
        assertEq(id == 0, within);
        assertEq(usdc.balanceOf(vendor) - before, within ? amount : 0);
    }

    /// @notice A random sequence of agent payments never spends more than the budget in one epoch.
    function testFuzz_agentNeverExceedsBudget(uint256[12] memory amounts) public {
        uint256 total;
        for (uint256 i; i < amounts.length; ++i) {
            uint256 a = bound(amounts[i], 1, 150 * USDC);
            (bool paid,) = _pay(a);
            if (paid) total += a;
            assertLe(total, 500 * USDC);
        }
        assertEq(usdc.balanceOf(vendor), total);
        assertEq(vault.getCategory(OPS).autoSpent, total);
    }

    /// @notice Budget / cap / period chosen at random: limits hold, and each epoch starts fresh.
    function testFuzz_limitsAcrossEpochs(uint96 budget, uint96 cap, uint32 period, uint256 seed) public {
        budget = uint96(bound(budget, 1, 1_000 * USDC));
        cap = uint96(bound(cap, 1, 1_000 * USDC));
        period = uint32(bound(period, 1 hours, 60 days));
        vm.prank(owner);
        vault.setCategory(ADS, budget, cap, period, bytes32(0));
        vm.prank(owner);
        vault.setPayee(ADS, vendor, true, bytes32(0));

        for (uint256 epochN; epochN < 3; ++epochN) {
            uint256 spentHere;
            for (uint256 i; i < 6; ++i) {
                uint256 a = bound(uint256(keccak256(abi.encode(seed, epochN, i))), 1, uint256(cap) * 2);
                vm.prank(agent);
                (bool paid,) = vault.pay(ADS, vendor, a, _h());
                if (paid) {
                    spentHere += a;
                    assertLe(a, cap);
                }
                assertLe(spentHere, budget);
            }
            assertEq(vault.remainingBudget(ADS), budget - spentHere);
            vm.warp(block.timestamp + period);
            assertEq(vault.remainingBudget(ADS), budget);
        }
    }

    function testFuzz_nonAllowlistedAlwaysReverts(address payee, uint256 amount) public {
        vm.assume(payee != vendor);
        amount = bound(amount, 1, 10_000 * USDC);
        vm.prank(agent);
        vm.expectRevert(abi.encodeWithSelector(PolicyVault.PayeeNotAllowed.selector, OPS, payee));
        vault.pay(OPS, payee, amount, _h());
    }

    function testFuzz_onlyAgentCanPay(address caller) public {
        vm.assume(caller != agent);
        vm.prank(caller);
        vm.expectRevert(PolicyVault.NotAgent.selector);
        vault.pay(OPS, vendor, 1, _h());
    }
}
