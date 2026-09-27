// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @title PulseTradeStamp — immutable decision receipts for Pulse Agent on Monad
/// @notice Anyone can stamp a decision hash; no admin, no upgrades.
contract PulseTradeStamp {
    uint8 public constant ACTION_HOLD = 0;
    uint8 public constant ACTION_BUY = 1;
    uint8 public constant ACTION_SELL = 2;

    struct Receipt {
        bytes32 decisionHash;
        uint256 symbolId;
        uint256 sizeHint;
        uint8 action;
        string note;
        uint64 stampedAt;
        address stamper;
    }

    uint256 public nextId = 1;
    mapping(uint256 => Receipt) private _receipts;

    event Stamped(
        uint256 indexed id,
        bytes32 indexed decisionHash,
        uint256 indexed symbolId,
        uint256 sizeHint,
        uint8 action,
        address stamper,
        uint64 stampedAt,
        string note
    );

    error ZeroHash();
    error BadAction();
    error NoteTooLong();
    error UnknownId();

    /// @param decisionHash keccak256 of agent audit record
    /// @param symbolId CMC id or opaque symbol key
    /// @param sizeHint notional/size hint (off-chain units; not enforced)
    /// @param action 0 hold / 1 buy / 2 sell
    /// @param note short free text (<= 280 bytes)
    function stamp(
        bytes32 decisionHash,
        uint256 symbolId,
        uint256 sizeHint,
        uint8 action,
        string calldata note
    ) external returns (uint256 id) {
        if (decisionHash == bytes32(0)) revert ZeroHash();
        if (action > ACTION_SELL) revert BadAction();
        if (bytes(note).length > 280) revert NoteTooLong();

        id = nextId++;
        uint64 ts = uint64(block.timestamp);
        _receipts[id] = Receipt({
            decisionHash: decisionHash,
            symbolId: symbolId,
            sizeHint: sizeHint,
            action: action,
            note: note,
            stampedAt: ts,
            stamper: msg.sender
        });
        emit Stamped(id, decisionHash, symbolId, sizeHint, action, msg.sender, ts, note);
    }

    function getReceipt(uint256 id) external view returns (Receipt memory) {
        if (id == 0 || id >= nextId) revert UnknownId();
        return _receipts[id];
    }
}
