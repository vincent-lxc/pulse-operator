// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @notice Minimal ERC-20 surface the vault needs. On Arc, native USDC exposes this
///         interface at 0x3600000000000000000000000000000000000000 (6 decimals).
interface IERC20Like {
    function transfer(address to, uint256 amount) external returns (bool);
    function balanceOf(address account) external view returns (uint256);
}

/// @title PolicyVault — on-chain spending policy for an AI operator agent
/// @notice Holds USDC and lets an `agent` key pay allowlisted payees only within
///         owner-set per-category budgets (per epoch) and per-transaction caps.
///         Anything over a limit is either escalated to the human `owner` as a
///         pending ApprovalRequest (no funds move) or reverted, depending on mode.
/// @dev    The limits live here, in contract storage, and are checked on every call.
///         They are NOT a prompt instruction: a jailbroken or buggy agent still cannot
///         move more than the policy allows. Every event carries a `decisionHash`
///         linking the on-chain action to the agent's off-chain audit record.
contract PolicyVault {
    // ------------------------------------------------------------------ types

    /// @notice What `pay` does when a payment exceeds the per-tx cap or epoch budget.
    enum OverLimitMode {
        Escalate, // create a pending ApprovalRequest, transfer nothing
        Revert // revert the call
    }

    enum RequestStatus {
        None,
        Pending,
        Approved,
        Rejected
    }

    /// @notice Why a payment was escalated / refused.
    uint8 public constant REASON_NONE = 0;
    uint8 public constant REASON_OVER_TX_CAP = 1;
    uint8 public constant REASON_OVER_BUDGET = 2;

    struct Category {
        bool enabled;
        uint256 budget; // max spend per epoch (token units, 6 decimals for USDC)
        uint256 perTxCap; // max single autonomous payment
        uint64 period; // epoch length in seconds (e.g. 7 days, 30 days)
        uint64 anchor; // epoch 0 starts here
        uint64 epoch; // epoch index the counters below belong to
        uint256 spent; // autoSpent + approvedSpent in `epoch`
        uint256 autoSpent; // paid autonomously by the agent in `epoch`
        uint256 approvedSpent; // paid via owner approval in `epoch`
    }

    struct ApprovalRequest {
        bytes32 category;
        address payee;
        uint256 amount;
        bytes32 decisionHash;
        uint64 createdAt;
        uint8 reason;
        RequestStatus status;
    }

    struct CategoryView {
        bool enabled;
        uint256 budget;
        uint256 perTxCap;
        uint64 period;
        uint64 epoch;
        uint64 epochStart;
        uint64 epochEnd;
        uint256 spent;
        uint256 autoSpent;
        uint256 approvedSpent;
        uint256 remaining;
    }

    struct Counters {
        uint256 decidedByAgent; // payments the agent executed within policy
        uint256 escalated; // payments turned into ApprovalRequests
        uint256 approved; // requests the owner approved (and paid)
        uint256 rejected; // requests the owner rejected
    }

    // ------------------------------------------------------------------ storage

    IERC20Like public immutable token;

    address public owner;
    address public pendingOwner;
    bytes32 private _pendingOwnerHash;
    address public agent;
    address public reserve;
    bool public paused;
    OverLimitMode public overLimitMode;

    mapping(bytes32 category => Category) private _categories;
    mapping(bytes32 category => mapping(address payee => bool)) public isPayeeAllowed;
    /// @notice decisionHash already consumed by `pay` (idempotency: a retried decision can't double-pay).
    mapping(bytes32 decisionHash => bool) public decisionUsed;

    uint256 public requestCount;
    mapping(uint256 requestId => ApprovalRequest) private _requests;
    uint256[] private _pendingIds;
    mapping(uint256 requestId => uint256 indexPlusOne) private _pendingIndex;

    Counters private _counters;

    uint256 private _lock = 1;

    // ------------------------------------------------------------------ events

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
    event CategoryConfigured(
        bytes32 indexed category, uint256 budget, uint256 perTxCap, uint64 period, bytes32 decisionHash
    );
    event CategoryDisabled(bytes32 indexed category, bytes32 decisionHash);
    event PayeeSet(bytes32 indexed category, address indexed payee, bool allowed, bytes32 decisionHash);
    event OverLimitModeSet(OverLimitMode mode, bytes32 decisionHash);
    event AgentSet(address indexed previousAgent, address indexed newAgent, bytes32 decisionHash);
    event ReserveSet(address indexed previousReserve, address indexed newReserve, bytes32 decisionHash);
    event SweptToReserve(address indexed reserve, uint256 amount, bytes32 decisionHash);
    event Paused(address indexed by, bytes32 decisionHash);
    event Unpaused(address indexed by, bytes32 decisionHash);
    event OwnershipTransferStarted(address indexed previousOwner, address indexed newOwner, bytes32 decisionHash);
    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner, bytes32 decisionHash);

    // ------------------------------------------------------------------ errors

    error NotOwner();
    error NotAgent();
    error NotPendingOwner();
    error IsPaused();
    error ZeroAddress();
    error ZeroAmount();
    error ZeroDecisionHash();
    error ZeroPeriod();
    error DecisionAlreadyUsed(bytes32 decisionHash);
    error UnknownCategory(bytes32 category);
    error PayeeNotAllowed(bytes32 category, address payee);
    error OverLimit(uint8 reason, uint256 amount, uint256 perTxCap, uint256 remaining);
    error RequestNotPending(uint256 requestId);
    error InsufficientBalance(uint256 balance, uint256 needed);
    error TransferFailed();
    error Reentrancy();

    // ------------------------------------------------------------------ modifiers

    modifier onlyOwner() {
        if (msg.sender != owner) revert NotOwner();
        _;
    }

    modifier onlyAgent() {
        if (msg.sender != agent) revert NotAgent();
        _;
    }

    modifier whenNotPaused() {
        if (paused) revert IsPaused();
        _;
    }

    modifier nonReentrant() {
        if (_lock != 1) revert Reentrancy();
        _lock = 2;
        _;
        _lock = 1;
    }

    // ------------------------------------------------------------------ constructor

    /// @param token_   ERC-20 held by the vault (Arc USDC: 0x3600000000000000000000000000000000000000).
    /// @param owner_   Human owner: sets policy, approves escalations, pauses, sweeps.
    /// @param agent_   Operator key the AI agent signs with. Can only call `pay`.
    /// @param reserve_ Where `sweepToReserve` sends funds (owner-controlled cold wallet).
    constructor(address token_, address owner_, address agent_, address reserve_) {
        if (token_ == address(0) || owner_ == address(0) || agent_ == address(0) || reserve_ == address(0)) {
            revert ZeroAddress();
        }
        token = IERC20Like(token_);
        owner = owner_;
        agent = agent_;
        reserve = reserve_;
        emit OwnershipTransferred(address(0), owner_, bytes32(0));
        emit AgentSet(address(0), agent_, bytes32(0));
        emit ReserveSet(address(0), reserve_, bytes32(0));
    }

    // ================================================================== agent

    /// @notice Agent pays `payee` from `category`'s budget.
    /// @return paid      true if funds were transferred now.
    /// @return requestId non-zero if the payment was escalated to the owner instead.
    function pay(bytes32 category, address payee, uint256 amount, bytes32 decisionHash)
        external
        onlyAgent
        whenNotPaused
        nonReentrant
        returns (bool paid, uint256 requestId)
    {
        if (decisionHash == bytes32(0)) revert ZeroDecisionHash();
        if (decisionUsed[decisionHash]) revert DecisionAlreadyUsed(decisionHash);
        if (amount == 0) revert ZeroAmount();
        Category storage c = _categories[category];
        if (!c.enabled) revert UnknownCategory(category);
        if (!isPayeeAllowed[category][payee]) revert PayeeNotAllowed(category, payee);

        _roll(c);
        decisionUsed[decisionHash] = true;

        uint8 reason = REASON_NONE;
        if (amount > c.perTxCap) {
            reason = REASON_OVER_TX_CAP;
        } else if (c.spent + amount > c.budget) {
            reason = REASON_OVER_BUDGET;
        }

        if (reason != REASON_NONE) {
            if (overLimitMode == OverLimitMode.Revert) {
                revert OverLimit(reason, amount, c.perTxCap, _remaining(c));
            }
            requestId = ++requestCount;
            _requests[requestId] = ApprovalRequest({
                category: category,
                payee: payee,
                amount: amount,
                decisionHash: decisionHash,
                createdAt: uint64(block.timestamp),
                reason: reason,
                status: RequestStatus.Pending
            });
            _pendingIds.push(requestId);
            _pendingIndex[requestId] = _pendingIds.length;
            unchecked {
                ++_counters.escalated;
            }
            emit ApprovalRequested(requestId, category, payee, amount, reason, decisionHash);
            return (false, requestId);
        }

        c.spent += amount;
        c.autoSpent += amount;
        unchecked {
            ++_counters.decidedByAgent;
        }
        emit AgentPaid(category, payee, amount, c.epoch, decisionHash);
        _transfer(payee, amount);
        return (true, 0);
    }

    // ================================================================== owner: escalations

    /// @notice Approve a pending request: transfers now and counts against the category's current-epoch budget.
    function approve(uint256 requestId) external onlyOwner whenNotPaused nonReentrant {
        ApprovalRequest storage r = _requests[requestId];
        if (r.status != RequestStatus.Pending) revert RequestNotPending(requestId);
        Category storage c = _categories[r.category];
        _roll(c);

        r.status = RequestStatus.Approved;
        _removePending(requestId);
        c.spent += r.amount;
        c.approvedSpent += r.amount;
        unchecked {
            ++_counters.approved;
        }
        emit RequestApproved(requestId, r.category, r.payee, r.amount, c.epoch, r.decisionHash);
        _transfer(r.payee, r.amount);
    }

    /// @notice Reject a pending request. No funds move. Allowed while paused.
    function reject(uint256 requestId) external onlyOwner {
        ApprovalRequest storage r = _requests[requestId];
        if (r.status != RequestStatus.Pending) revert RequestNotPending(requestId);
        r.status = RequestStatus.Rejected;
        _removePending(requestId);
        unchecked {
            ++_counters.rejected;
        }
        emit RequestRejected(requestId, r.category, r.payee, r.amount, r.decisionHash);
    }

    // ================================================================== owner: policy

    /// @notice Create or update a category. Changing `period` (or creating) restarts the epoch
    ///         and resets its counters; changing only budget / cap keeps current-epoch spend.
    /// @param decisionHash optional hash of the owner's rationale / off-chain record (may be zero).
    function setCategory(bytes32 category, uint256 budget, uint256 perTxCap, uint64 period, bytes32 decisionHash)
        external
        onlyOwner
    {
        if (period == 0) revert ZeroPeriod();
        Category storage c = _categories[category];
        if (c.period != period) {
            c.period = period;
            c.anchor = uint64(block.timestamp);
            c.epoch = 0;
            c.spent = 0;
            c.autoSpent = 0;
            c.approvedSpent = 0;
        } else {
            _roll(c);
        }
        c.enabled = true;
        c.budget = budget;
        c.perTxCap = perTxCap;
        emit CategoryConfigured(category, budget, perTxCap, period, decisionHash);
    }

    /// @notice Disable a category: the agent can no longer pay from it. Pending requests can still be decided.
    function disableCategory(bytes32 category, bytes32 decisionHash) external onlyOwner {
        Category storage c = _categories[category];
        if (!c.enabled) revert UnknownCategory(category);
        c.enabled = false;
        emit CategoryDisabled(category, decisionHash);
    }

    function setPayee(bytes32 category, address payee, bool allowed, bytes32 decisionHash) external onlyOwner {
        if (payee == address(0)) revert ZeroAddress();
        isPayeeAllowed[category][payee] = allowed;
        emit PayeeSet(category, payee, allowed, decisionHash);
    }

    function setOverLimitMode(OverLimitMode mode, bytes32 decisionHash) external onlyOwner {
        overLimitMode = mode;
        emit OverLimitModeSet(mode, decisionHash);
    }

    /// @notice Rotate (or effectively revoke, by rotating to a burn key) the agent operator key.
    function setAgent(address newAgent, bytes32 decisionHash) external onlyOwner {
        if (newAgent == address(0)) revert ZeroAddress();
        emit AgentSet(agent, newAgent, decisionHash);
        agent = newAgent;
    }

    function setReserve(address newReserve, bytes32 decisionHash) external onlyOwner {
        if (newReserve == address(0)) revert ZeroAddress();
        emit ReserveSet(reserve, newReserve, decisionHash);
        reserve = newReserve;
    }

    // ================================================================== owner: safety

    function pause() external onlyOwner {
        _pause(bytes32(0));
    }

    function pause(bytes32 decisionHash) external onlyOwner {
        _pause(decisionHash);
    }

    function unpause() external onlyOwner {
        _unpause(bytes32(0));
    }

    function unpause(bytes32 decisionHash) external onlyOwner {
        _unpause(decisionHash);
    }

    /// @notice Move funds to the owner-set reserve. Deliberately allowed while paused:
    ///         pulling funds to cold storage is the fail-safe direction.
    function sweepToReserve(uint256 amount) external onlyOwner nonReentrant {
        _sweep(amount, bytes32(0));
    }

    function sweepToReserve(uint256 amount, bytes32 decisionHash) external onlyOwner nonReentrant {
        _sweep(amount, decisionHash);
    }

    function transferOwnership(address newOwner, bytes32 decisionHash) external onlyOwner {
        if (newOwner == address(0)) revert ZeroAddress();
        pendingOwner = newOwner;
        _pendingOwnerHash = decisionHash;
        emit OwnershipTransferStarted(owner, newOwner, decisionHash);
    }

    function acceptOwnership() external {
        if (msg.sender != pendingOwner) revert NotPendingOwner();
        bytes32 h = _pendingOwnerHash;
        emit OwnershipTransferred(owner, msg.sender, h);
        owner = msg.sender;
        pendingOwner = address(0);
        _pendingOwnerHash = bytes32(0);
    }

    // ================================================================== views

    function balance() external view returns (uint256) {
        return token.balanceOf(address(this));
    }

    /// @notice Budget left for autonomous agent payments in the category's current epoch.
    function remainingBudget(bytes32 category) external view returns (uint256) {
        Category memory c = _categories[category];
        if (!c.enabled) return 0;
        _rollView(c);
        return c.spent >= c.budget ? 0 : c.budget - c.spent;
    }

    function getCategory(bytes32 category) external view returns (CategoryView memory v) {
        Category memory c = _categories[category];
        if (c.period == 0) return v;
        _rollView(c);
        v.enabled = c.enabled;
        v.budget = c.budget;
        v.perTxCap = c.perTxCap;
        v.period = c.period;
        v.epoch = c.epoch;
        v.epochStart = c.anchor + c.epoch * c.period;
        v.epochEnd = v.epochStart + c.period;
        v.spent = c.spent;
        v.autoSpent = c.autoSpent;
        v.approvedSpent = c.approvedSpent;
        v.remaining = c.spent >= c.budget ? 0 : c.budget - c.spent;
    }

    function getRequest(uint256 requestId) external view returns (ApprovalRequest memory) {
        return _requests[requestId];
    }

    function pendingCount() external view returns (uint256) {
        return _pendingIds.length;
    }

    function pendingRequestIds() external view returns (uint256[] memory) {
        return _pendingIds;
    }

    function counters() external view returns (Counters memory) {
        return _counters;
    }

    // ================================================================== internal

    function _currentEpoch(Category memory c) private view returns (uint64) {
        // forge-lint: disable-next-line(unsafe-typecast)
        return uint64((block.timestamp - c.anchor) / c.period); // epoch index fits easily in uint64
    }

    function _roll(Category storage c) private {
        // forge-lint: disable-next-line(unsafe-typecast)
        uint64 e = uint64((block.timestamp - c.anchor) / c.period); // epoch index fits easily in uint64
        if (e != c.epoch) {
            c.epoch = e;
            c.spent = 0;
            c.autoSpent = 0;
            c.approvedSpent = 0;
        }
    }

    function _rollView(Category memory c) private view {
        uint64 e = _currentEpoch(c);
        if (e != c.epoch) {
            c.epoch = e;
            c.spent = 0;
            c.autoSpent = 0;
            c.approvedSpent = 0;
        }
    }

    function _remaining(Category storage c) private view returns (uint256) {
        return c.spent >= c.budget ? 0 : c.budget - c.spent;
    }

    function _removePending(uint256 requestId) private {
        uint256 idx = _pendingIndex[requestId];
        uint256 last = _pendingIds.length;
        if (idx != last) {
            uint256 moved = _pendingIds[last - 1];
            _pendingIds[idx - 1] = moved;
            _pendingIndex[moved] = idx;
        }
        _pendingIds.pop();
        delete _pendingIndex[requestId];
    }

    function _pause(bytes32 decisionHash) private {
        paused = true;
        emit Paused(msg.sender, decisionHash);
    }

    function _unpause(bytes32 decisionHash) private {
        paused = false;
        emit Unpaused(msg.sender, decisionHash);
    }

    function _sweep(uint256 amount, bytes32 decisionHash) private {
        if (amount == 0) revert ZeroAmount();
        emit SweptToReserve(reserve, amount, decisionHash);
        _transfer(reserve, amount);
    }

    /// @dev Handles tokens that return bool, return nothing, or revert.
    function _transfer(address to, uint256 amount) private {
        uint256 bal = token.balanceOf(address(this));
        if (bal < amount) revert InsufficientBalance(bal, amount);
        (bool ok, bytes memory ret) = address(token).call(abi.encodeCall(IERC20Like.transfer, (to, amount)));
        if (!ok || (ret.length != 0 && !abi.decode(ret, (bool)))) revert TransferFailed();
    }
}
