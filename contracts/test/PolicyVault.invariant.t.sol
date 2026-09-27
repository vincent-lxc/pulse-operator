// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {PolicyVault} from "../src/PolicyVault.sol";
import {MockERC20} from "./mocks/MockERC20.sol";

/// @notice Drives random agent payments, owner approvals / rejections, sweeps and time jumps.
contract PolicyVaultHandler is Test {
    PolicyVault public vault;
    MockERC20 public usdc;
    address public owner;
    address public agent;

    bytes32[2] public cats = [bytes32("ops"), bytes32("ads")];
    address[3] public payees;
    uint256 internal nonce;

    // ghost accounting, keyed by (category, epoch)
    mapping(bytes32 => mapping(uint64 => uint256)) public ghostAuto;
    mapping(bytes32 => mapping(uint64 => uint256)) public ghostApproved;
    uint256 public ghostPaidOut;
    uint256 public ghostSwept;
    uint256 public maxEpochSeen;

    constructor(PolicyVault v, MockERC20 t, address o, address a, address[3] memory p) {
        vault = v;
        usdc = t;
        owner = o;
        agent = a;
        payees = p;
    }

    function pay(uint256 catSeed, uint256 payeeSeed, uint256 amount) external {
        bytes32 cat = cats[catSeed % 2];
        address payee = payees[payeeSeed % 3];
        amount = bound(amount, 1, 150e6);
        if (!vault.isPayeeAllowed(cat, payee)) return;
        if (usdc.balanceOf(address(vault)) < amount) return;
        uint64 e = vault.getCategory(cat).epoch;
        vm.prank(agent);
        (bool paid,) = vault.pay(cat, payee, amount, keccak256(abi.encode(++nonce)));
        if (paid) {
            ghostAuto[cat][e] += amount;
            ghostPaidOut += amount;
        }
    }

    function approve(uint256 seed) external {
        uint256[] memory ids = vault.pendingRequestIds();
        if (ids.length == 0) return;
        uint256 id = ids[seed % ids.length];
        PolicyVault.ApprovalRequest memory r = vault.getRequest(id);
        if (usdc.balanceOf(address(vault)) < r.amount) return;
        uint64 e = vault.getCategory(r.category).epoch;
        vm.prank(owner);
        vault.approve(id);
        ghostApproved[r.category][e] += r.amount;
        ghostPaidOut += r.amount;
    }

    function reject(uint256 seed) external {
        uint256[] memory ids = vault.pendingRequestIds();
        if (ids.length == 0) return;
        vm.prank(owner);
        vault.reject(ids[seed % ids.length]);
    }

    function sweep(uint256 amount) external {
        uint256 bal = usdc.balanceOf(address(vault));
        if (bal == 0) return;
        amount = bound(amount, 1, bal / 10 + 1);
        if (amount > bal) return;
        vm.prank(owner);
        vault.sweepToReserve(amount);
        ghostSwept += amount;
    }

    function warp(uint256 secs) external {
        vm.warp(block.timestamp + bound(secs, 1 minutes, 1 days));
        uint64 e = vault.getCategory(cats[0]).epoch;
        if (e > maxEpochSeen) maxEpochSeen = e;
    }
}

contract PolicyVaultInvariantTest is Test {
    PolicyVault internal vault;
    MockERC20 internal usdc;
    PolicyVaultHandler internal handler;

    address internal owner = makeAddr("owner");
    address internal agent = makeAddr("agent");
    address internal reserve = makeAddr("reserve");
    uint256 internal constant FUNDED = 1_000_000e6;
    uint256 internal constant OPS_BUDGET = 500e6;
    uint256 internal constant ADS_BUDGET = 200e6;

    function setUp() public {
        vm.warp(1_800_000_000);
        usdc = new MockERC20();
        vault = new PolicyVault(address(usdc), owner, agent, reserve);
        usdc.mint(address(vault), FUNDED);

        address[3] memory p = [makeAddr("p0"), makeAddr("p1"), makeAddr("p2")];
        vm.startPrank(owner);
        vault.setCategory("ops", OPS_BUDGET, 120e6, 7 days, bytes32(0));
        vault.setCategory("ads", ADS_BUDGET, 80e6, 1 days, bytes32(0));
        vault.setPayee("ops", p[0], true, bytes32(0));
        vault.setPayee("ops", p[1], true, bytes32(0));
        vault.setPayee("ads", p[1], true, bytes32(0));
        vault.setPayee("ads", p[2], true, bytes32(0));
        vm.stopPrank();

        handler = new PolicyVaultHandler(vault, usdc, owner, agent, p);
        targetContract(address(handler));
    }

    /// @notice Core policy invariant: in any category and epoch, what the agent spent on its own
    ///         never exceeds the budget. Only owner-approved requests may push total spend above it.
    /// forge-config: default.invariant.runs = 64
    /// forge-config: default.invariant.depth = 500
    function invariant_agentSpendWithinBudget() public view {
        _checkCat("ops", OPS_BUDGET);
        _checkCat("ads", ADS_BUDGET);
    }

    function _checkCat(bytes32 cat, uint256 budget) internal view {
        PolicyVault.CategoryView memory v = vault.getCategory(cat);
        assertLe(v.autoSpent, budget, "agent overspent current epoch");
        assertEq(v.spent, v.autoSpent + v.approvedSpent);
        if (v.spent > budget) assertGt(v.approvedSpent, 0, "over budget without owner approval");
        // ghost check over every epoch seen so far (ops epochs are 7d, ads 1d)
        uint64 last = v.epoch;
        for (uint64 e; e <= last; ++e) {
            assertLe(handler.ghostAuto(cat, e), budget, "ghost: agent overspent a past epoch");
        }
        // current epoch agrees with the handler's own bookkeeping
        assertEq(v.autoSpent, handler.ghostAuto(cat, last));
        assertEq(v.approvedSpent, handler.ghostApproved(cat, last));
    }

    /// @notice Funds are conserved: vault balance = funded − paid out − swept.
    /// forge-config: default.invariant.runs = 64
    /// forge-config: default.invariant.depth = 500
    function invariant_conservation() public view {
        assertEq(usdc.balanceOf(address(vault)), FUNDED - handler.ghostPaidOut() - handler.ghostSwept());
        assertEq(usdc.balanceOf(reserve), handler.ghostSwept());
    }

    /// @notice Counters are consistent with the request book.
    /// forge-config: default.invariant.runs = 64
    /// forge-config: default.invariant.depth = 500
    function invariant_counters() public view {
        PolicyVault.Counters memory c = vault.counters();
        assertEq(c.escalated, vault.requestCount());
        assertEq(c.escalated, c.approved + c.rejected + vault.pendingCount());
    }
}
