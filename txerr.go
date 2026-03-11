package globalrpc

import (
	"errors"
	"strings"
)

// The error are based on and extracted from the go-ethereum v1.17.1 error definitions
type TxErrKind int

const (
	TxErrUnknown TxErrKind = iota // unrecognized error

	// nonce errors (https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/error.go)
	TxErrNonceTooLow  // "nonce too low"
	TxErrNonceTooHigh // "nonce too high" (blob pool nonce gap)
	TxErrNonceMax     // "nonce has max value" (EIP-2681)

	// pool errors (https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/txpool/errors.go)
	TxErrAlreadyKnown       // "already known"
	TxErrUnderpriced        // "transaction underpriced" (fee too low to enter pool)
	TxErrReplaceUnderpriced // "replacement transaction underpriced"
	TxErrAccountLimit       // "account limit exceeded"
	TxErrPoolFull           // "txpool is full" (legacypool)
	TxErrAlreadyReserved    // "address already reserved" (blob/non-blob conflict)
	TxErrInflightLimit      // "in-flight transaction limit reached for delegated accounts"

	// gas errors (https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/error.go)
	TxErrIntrinsicGas    // "intrinsic gas too low"
	TxErrFloorDataGas    // "insufficient gas for floor data gas cost"
	TxErrGasLimitReached // "gas limit reached" (block gas pool exhausted)
	TxErrGasLimitTooHigh // "transaction gas limit too high"
	TxErrGasUintOverflow // "gas uint64 overflow"
	TxErrGasLimit        // "exceeds block gas limit"

	// fee errors (https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/error.go, https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/types/transaction.go)
	TxErrFeeCapTooLow   // "max fee per gas less than block base fee" or "fee cap less than base fee"
	TxErrTipAboveFeeCap // "max priority fee per gas higher than max fee per gas"
	TxErrTipVeryHigh    // "max priority fee per gas higher than 2^256-1"
	TxErrFeeCapVeryHigh // "max fee per gas higher than 2^256-1"
	TxErrGasPriceTooLow // "transaction gas price below minimum"

	// fund errors (https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/error.go)
	TxErrInsufficientFunds            // "insufficient funds for gas * price + value"
	TxErrInsufficientFundsForTransfer // "insufficient funds for transfer"

	// sender/type errors (https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/error.go, https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/types/transaction.go)
	TxErrTxTypeNotSupported // "transaction type not supported"
	TxErrSenderNoEOA        // "sender not an eoa"
	TxErrInvalidSender      // "invalid sender"
	TxErrInvalidSig         // "invalid transaction v, r, s values"

	// size/data errors (https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/error.go, https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/txpool/errors.go)
	TxErrMaxInitCodeSize // "max initcode size exceeded"
	TxErrOversizedData   // "oversized data"
	TxErrNegativeValue   // "negative value"

	// EIP-4844 blob errors (https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/error.go, https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/txpool/errors.go)
	TxErrBlobFeeCapTooLow  // "max fee per blob gas less than block blob gas fee"
	TxErrMissingBlobHashes // "blob transaction missing blob hashes"
	TxErrTooManyBlobs      // "blob transaction has too many blobs"
	TxErrBlobTxCreate      // "blob transaction of type create"
	TxErrBlobLimitExceeded // "transaction blob limit exceeded"

	// EIP-7702 errors (https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/error.go)
	TxErrEmptyAuthList   // "EIP-7702 transaction with empty auth list"
	TxErrSetCodeTxCreate // "EIP-7702 transaction cannot be used to create contract"

	// legacypool/ethapi errors (https://github.com/ethereum/go-ethereum/blob/v1.17.1/core/txpool/legacypool/legacypool.go, https://github.com/ethereum/go-ethereum/blob/v1.17.1/internal/ethapi/api.go)
	TxErrFutureReplacePending  // "future transaction tries to replace pending"
	TxErrOutOfOrderTxDelegated // "gapped-nonce tx from delegated accounts"
	TxErrAuthorityReserved     // "authority already reserved"
	TxErrEIP155Required        // "only replay-protected (EIP-155) transactions allowed over RPC"
	TxErrTxFeeTooHigh          // "tx fee exceeds the configured cap"
	TxErrKZGVerificationError  // "KZG verification error"
	TxErrBlobSidecarConvFailed // "blob sidecar conversion failed"
	TxErrUint256Overflow       // "bigint overflow, too large for uint256"
	TxErrUnexpectedProtection  // "transaction type does not supported EIP-155 protected signatures"
)

func (k TxErrKind) String() string {
	switch k {
	case TxErrNonceTooLow:
		return "NonceTooLow"
	case TxErrNonceTooHigh:
		return "NonceTooHigh"
	case TxErrNonceMax:
		return "NonceMax"
	case TxErrAlreadyKnown:
		return "AlreadyKnown"
	case TxErrUnderpriced:
		return "Underpriced"
	case TxErrReplaceUnderpriced:
		return "ReplaceUnderpriced"
	case TxErrAccountLimit:
		return "AccountLimit"
	case TxErrPoolFull:
		return "PoolFull"
	case TxErrAlreadyReserved:
		return "AlreadyReserved"
	case TxErrInflightLimit:
		return "InflightLimit"
	case TxErrIntrinsicGas:
		return "IntrinsicGas"
	case TxErrFloorDataGas:
		return "FloorDataGas"
	case TxErrGasLimitReached:
		return "GasLimitReached"
	case TxErrGasLimitTooHigh:
		return "GasLimitTooHigh"
	case TxErrGasUintOverflow:
		return "GasUintOverflow"
	case TxErrGasLimit:
		return "GasLimit"
	case TxErrFeeCapTooLow:
		return "FeeCapTooLow"
	case TxErrTipAboveFeeCap:
		return "TipAboveFeeCap"
	case TxErrTipVeryHigh:
		return "TipVeryHigh"
	case TxErrFeeCapVeryHigh:
		return "FeeCapVeryHigh"
	case TxErrGasPriceTooLow:
		return "GasPriceTooLow"
	case TxErrInsufficientFunds:
		return "InsufficientFunds"
	case TxErrInsufficientFundsForTransfer:
		return "InsufficientFundsForTransfer"
	case TxErrTxTypeNotSupported:
		return "TxTypeNotSupported"
	case TxErrSenderNoEOA:
		return "SenderNoEOA"
	case TxErrInvalidSender:
		return "InvalidSender"
	case TxErrInvalidSig:
		return "InvalidSig"
	case TxErrMaxInitCodeSize:
		return "MaxInitCodeSize"
	case TxErrOversizedData:
		return "OversizedData"
	case TxErrNegativeValue:
		return "NegativeValue"
	case TxErrBlobFeeCapTooLow:
		return "BlobFeeCapTooLow"
	case TxErrMissingBlobHashes:
		return "MissingBlobHashes"
	case TxErrTooManyBlobs:
		return "TooManyBlobs"
	case TxErrBlobTxCreate:
		return "BlobTxCreate"
	case TxErrBlobLimitExceeded:
		return "BlobLimitExceeded"
	case TxErrEmptyAuthList:
		return "EmptyAuthList"
	case TxErrSetCodeTxCreate:
		return "SetCodeTxCreate"
	case TxErrFutureReplacePending:
		return "FutureReplacePending"
	case TxErrOutOfOrderTxDelegated:
		return "OutOfOrderTxDelegated"
	case TxErrAuthorityReserved:
		return "AuthorityReserved"
	case TxErrEIP155Required:
		return "EIP155Required"
	case TxErrTxFeeTooHigh:
		return "TxFeeTooHigh"
	case TxErrKZGVerificationError:
		return "KZGVerificationError"
	case TxErrBlobSidecarConvFailed:
		return "BlobSidecarConvFailed"
	case TxErrUint256Overflow:
		return "Uint256Overflow"
	case TxErrUnexpectedProtection:
		return "UnexpectedProtection"
	default:
		return "Unknown"
	}
}

type TxError struct {
	Kind TxErrKind
	Err  error
}

func (e *TxError) Error() string {
	return e.Err.Error()
}

func (e *TxError) Unwrap() error {
	return e.Err
}

type RpcErrKind int

const (
	RpcErrUnknown    RpcErrKind = iota
	RpcErrLock                  // couldn't acquire RPC lock
	RpcErrDial                  // couldn't connect to RPC node
	RpcErrConnection            // connection dropped during call
)

func (k RpcErrKind) String() string {
	switch k {
	case RpcErrLock:
		return "Lock"
	case RpcErrDial:
		return "Dial"
	case RpcErrConnection:
		return "Connection"
	default:
		return "Unknown"
	}
}
type RpcError struct {
	Kind RpcErrKind
	Err  error
}

func (e *RpcError) Error() string {
	return e.Err.Error()
}

func (e *RpcError) Unwrap() error {
	return e.Err
}

func ClassifyTxErr(err error) TxErrKind {
	if err == nil {
		return TxErrUnknown
	}
	var txErr *TxError
	if errors.As(err, &txErr) {
		return txErr.Kind
	}
	msg := strings.ToLower(err.Error())

	switch {
	// nonce
	case strings.Contains(msg, "nonce too low"):
		return TxErrNonceTooLow
	case strings.Contains(msg, "nonce too high"):
		return TxErrNonceTooHigh
	case strings.Contains(msg, "nonce has max value"):
		return TxErrNonceMax

	// pool
	case strings.Contains(msg, "already known"):
		return TxErrAlreadyKnown
	case strings.Contains(msg, "replacement transaction underpriced"):
		return TxErrReplaceUnderpriced
	case strings.Contains(msg, "transaction underpriced"):
		return TxErrUnderpriced
	case strings.Contains(msg, "account limit exceeded"):
		return TxErrAccountLimit
	case strings.Contains(msg, "txpool is full"):
		return TxErrPoolFull
	case strings.Contains(msg, "address already reserved"):
		return TxErrAlreadyReserved
	case strings.Contains(msg, "in-flight transaction limit reached"):
		return TxErrInflightLimit

	// gas
	case strings.Contains(msg, "intrinsic gas too low"):
		return TxErrIntrinsicGas
	case strings.Contains(msg, "insufficient gas for floor data gas cost"):
		return TxErrFloorDataGas
	case strings.Contains(msg, "gas limit reached"):
		return TxErrGasLimitReached
	case strings.Contains(msg, "transaction gas limit too high"):
		return TxErrGasLimitTooHigh
	case strings.Contains(msg, "gas uint64 overflow"):
		return TxErrGasUintOverflow
	case strings.Contains(msg, "exceeds block gas limit"):
		return TxErrGasLimit

	// fees . order matters here.
	case strings.Contains(msg, "max fee per gas less than block base fee") ||
		strings.Contains(msg, "fee cap less than base fee"):
		return TxErrFeeCapTooLow
	case strings.Contains(msg, "max priority fee per gas higher than max fee per gas"):
		return TxErrTipAboveFeeCap
	case strings.Contains(msg, "max priority fee per gas higher than 2^256-1"):
		return TxErrTipVeryHigh
	case strings.Contains(msg, "max fee per gas higher than 2^256-1"):
		return TxErrFeeCapVeryHigh
	case strings.Contains(msg, "transaction gas price below minimum"):
		return TxErrGasPriceTooLow

	// funds . order matters here too.
	case strings.Contains(msg, "insufficient funds for transfer"):
		return TxErrInsufficientFundsForTransfer
	case strings.Contains(msg, "insufficient funds"):
		return TxErrInsufficientFunds

	// sender/type
	case strings.Contains(msg, "transaction type not supported"):
		return TxErrTxTypeNotSupported
	case strings.Contains(msg, "sender not an eoa"):
		return TxErrSenderNoEOA
	case strings.Contains(msg, "invalid sender"):
		return TxErrInvalidSender
	case strings.Contains(msg, "invalid transaction v, r, s values"):
		return TxErrInvalidSig

	// size/data
	case strings.Contains(msg, "max initcode size exceeded"):
		return TxErrMaxInitCodeSize
	case strings.Contains(msg, "oversized data"):
		return TxErrOversizedData
	case strings.Contains(msg, "negative value"):
		return TxErrNegativeValue

	// EIP-4844 blobs
	case strings.Contains(msg, "max fee per blob gas less than block blob gas fee"):
		return TxErrBlobFeeCapTooLow
	case strings.Contains(msg, "blob transaction missing blob hashes"):
		return TxErrMissingBlobHashes
	case strings.Contains(msg, "blob transaction has too many blobs"):
		return TxErrTooManyBlobs
	case strings.Contains(msg, "blob transaction of type create"):
		return TxErrBlobTxCreate
	case strings.Contains(msg, "transaction blob limit exceeded"):
		return TxErrBlobLimitExceeded

	// EIP-7702
	case strings.Contains(msg, "eip-7702 transaction with empty auth list"):
		return TxErrEmptyAuthList
	case strings.Contains(msg, "eip-7702 transaction cannot be used to create contract"):
		return TxErrSetCodeTxCreate

	// legacypool / ethapi
	case strings.Contains(msg, "future transaction tries to replace pending"):
		return TxErrFutureReplacePending
	case strings.Contains(msg, "gapped-nonce tx from delegated accounts"):
		return TxErrOutOfOrderTxDelegated
	case strings.Contains(msg, "authority already reserved"):
		return TxErrAuthorityReserved
	case strings.Contains(msg, "only replay-protected"):
		return TxErrEIP155Required
	case strings.Contains(msg, "tx fee") && strings.Contains(msg, "exceeds the configured cap"):
		return TxErrTxFeeTooHigh
	case strings.Contains(msg, "kzg verification error"):
		return TxErrKZGVerificationError
	case strings.Contains(msg, "blob sidecar conversion failed"):
		return TxErrBlobSidecarConvFailed
	case strings.Contains(msg, "too large for uint256"):
		return TxErrUint256Overflow
	case strings.Contains(msg, "does not supported eip-155 protected signatures"):
		return TxErrUnexpectedProtection

	default:
		return TxErrUnknown
	}
}

func IsNonceTooLow(err error) bool        { return ClassifyTxErr(err) == TxErrNonceTooLow }
func IsNonceTooHigh(err error) bool       { return ClassifyTxErr(err) == TxErrNonceTooHigh }
func IsNonceMax(err error) bool           { return ClassifyTxErr(err) == TxErrNonceMax }
func IsAlreadyKnown(err error) bool       { return ClassifyTxErr(err) == TxErrAlreadyKnown }
func IsUnderpriced(err error) bool        { return ClassifyTxErr(err) == TxErrUnderpriced }
func IsReplaceUnderpriced(err error) bool { return ClassifyTxErr(err) == TxErrReplaceUnderpriced }
func IsAccountLimit(err error) bool       { return ClassifyTxErr(err) == TxErrAccountLimit }
func IsPoolFull(err error) bool           { return ClassifyTxErr(err) == TxErrPoolFull }
func IsAlreadyReserved(err error) bool    { return ClassifyTxErr(err) == TxErrAlreadyReserved }
func IsInflightLimit(err error) bool      { return ClassifyTxErr(err) == TxErrInflightLimit }
func IsIntrinsicGas(err error) bool       { return ClassifyTxErr(err) == TxErrIntrinsicGas }
func IsFloorDataGas(err error) bool       { return ClassifyTxErr(err) == TxErrFloorDataGas }
func IsGasLimitReached(err error) bool    { return ClassifyTxErr(err) == TxErrGasLimitReached }
func IsGasLimitTooHigh(err error) bool    { return ClassifyTxErr(err) == TxErrGasLimitTooHigh }
func IsGasUintOverflow(err error) bool    { return ClassifyTxErr(err) == TxErrGasUintOverflow }
func IsGasLimit(err error) bool           { return ClassifyTxErr(err) == TxErrGasLimit }
func IsFeeCapTooLow(err error) bool       { return ClassifyTxErr(err) == TxErrFeeCapTooLow }
func IsTipAboveFeeCap(err error) bool     { return ClassifyTxErr(err) == TxErrTipAboveFeeCap }
func IsTipVeryHigh(err error) bool        { return ClassifyTxErr(err) == TxErrTipVeryHigh }
func IsFeeCapVeryHigh(err error) bool     { return ClassifyTxErr(err) == TxErrFeeCapVeryHigh }
func IsGasPriceTooLow(err error) bool     { return ClassifyTxErr(err) == TxErrGasPriceTooLow }
func IsInsufficientFunds(err error) bool  { return ClassifyTxErr(err) == TxErrInsufficientFunds }
func IsInsufficientFundsForTransfer(err error) bool {
	return ClassifyTxErr(err) == TxErrInsufficientFundsForTransfer
}
func IsTxTypeNotSupported(err error) bool    { return ClassifyTxErr(err) == TxErrTxTypeNotSupported }
func IsSenderNoEOA(err error) bool           { return ClassifyTxErr(err) == TxErrSenderNoEOA }
func IsInvalidSender(err error) bool         { return ClassifyTxErr(err) == TxErrInvalidSender }
func IsInvalidSig(err error) bool            { return ClassifyTxErr(err) == TxErrInvalidSig }
func IsMaxInitCodeSize(err error) bool       { return ClassifyTxErr(err) == TxErrMaxInitCodeSize }
func IsOversizedData(err error) bool         { return ClassifyTxErr(err) == TxErrOversizedData }
func IsNegativeValue(err error) bool         { return ClassifyTxErr(err) == TxErrNegativeValue }
func IsBlobFeeCapTooLow(err error) bool      { return ClassifyTxErr(err) == TxErrBlobFeeCapTooLow }
func IsMissingBlobHashes(err error) bool     { return ClassifyTxErr(err) == TxErrMissingBlobHashes }
func IsTooManyBlobs(err error) bool          { return ClassifyTxErr(err) == TxErrTooManyBlobs }
func IsBlobTxCreate(err error) bool          { return ClassifyTxErr(err) == TxErrBlobTxCreate }
func IsBlobLimitExceeded(err error) bool     { return ClassifyTxErr(err) == TxErrBlobLimitExceeded }
func IsEmptyAuthList(err error) bool         { return ClassifyTxErr(err) == TxErrEmptyAuthList }
func IsSetCodeTxCreate(err error) bool       { return ClassifyTxErr(err) == TxErrSetCodeTxCreate }
func IsFutureReplacePending(err error) bool  { return ClassifyTxErr(err) == TxErrFutureReplacePending }
func IsOutOfOrderTxDelegated(err error) bool { return ClassifyTxErr(err) == TxErrOutOfOrderTxDelegated }
func IsAuthorityReserved(err error) bool     { return ClassifyTxErr(err) == TxErrAuthorityReserved }
func IsEIP155Required(err error) bool        { return ClassifyTxErr(err) == TxErrEIP155Required }
func IsTxFeeTooHigh(err error) bool          { return ClassifyTxErr(err) == TxErrTxFeeTooHigh }
func IsKZGVerificationError(err error) bool  { return ClassifyTxErr(err) == TxErrKZGVerificationError }
func IsBlobSidecarConvFailed(err error) bool { return ClassifyTxErr(err) == TxErrBlobSidecarConvFailed }
func IsUint256Overflow(err error) bool       { return ClassifyTxErr(err) == TxErrUint256Overflow }
func IsUnexpectedProtection(err error) bool  { return ClassifyTxErr(err) == TxErrUnexpectedProtection }
