// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @title PulseReceipt
/// @notice Tiny immutable receipt registry for Pulse-style market alerts on Arc.
/// @dev Companion to Pulse Screener (Circle / DoraHacks Arc Microgrants).
///      Events are the canonical record; storage is a convenience read path
///      so a UI can work without a Pulse Go indexer.
contract PulseReceipt {
    /// @notice Pulse-style market alert fired off-chain, then stamped here.
    uint8 public constant KIND_ALERT = 0;
    /// @notice Manual stamp by a user (no automated alert).
    uint8 public constant KIND_MANUAL = 1;

    /// @notice Keep notes short — this is a receipt, not a blog.
    uint256 public constant MAX_NOTE_BYTES = 280;

    event ReceiptStamped(
        address indexed stamper,
        uint256 indexed cmcId,
        bytes32 alertHash,
        int256 threshold,
        uint8 kind,
        string note,
        uint256 stampedAt
    );

    struct Receipt {
        address stamper;
        uint256 cmcId;
        bytes32 alertHash;
        int256 threshold;
        uint8 kind;
        string note;
        uint256 stampedAt;
    }

    uint256 public receiptCount;

    mapping(uint256 id => Receipt) private _receipts;

    error EmptyAlertHash();
    error NoteTooLong();
    error UnknownKind();
    error ReceiptNotFound();

    /// @notice Stamp an immutable receipt on Arc.
    /// @param cmcId CoinMarketCap numeric id (BTC = 1, ETH = 1027, …).
    /// @param alertHash Content hash of the off-chain alert / payload. Must be non-zero.
    /// @param threshold Signed threshold the alert used (e.g. +500 / -300 Pulse units).
    /// @param kind KIND_ALERT (0) or KIND_MANUAL (1).
    /// @param note Optional short memo. Empty string is allowed.
    /// @return id 1-based receipt id.
    function stampReceipt(uint256 cmcId, bytes32 alertHash, int256 threshold, uint8 kind, string calldata note)
        external
        returns (uint256 id)
    {
        if (alertHash == bytes32(0)) revert EmptyAlertHash();
        if (kind > KIND_MANUAL) revert UnknownKind();
        if (bytes(note).length > MAX_NOTE_BYTES) revert NoteTooLong();

        id = ++receiptCount;
        uint256 stampedAt = block.timestamp;

        _receipts[id] = Receipt({
            stamper: msg.sender,
            cmcId: cmcId,
            alertHash: alertHash,
            threshold: threshold,
            kind: kind,
            note: note,
            stampedAt: stampedAt
        });

        emit ReceiptStamped(msg.sender, cmcId, alertHash, threshold, kind, note, stampedAt);
    }

    /// @notice Read a stored receipt by id.
    function getReceipt(uint256 id) external view returns (Receipt memory) {
        if (id == 0 || id > receiptCount) revert ReceiptNotFound();
        return _receipts[id];
    }
}
