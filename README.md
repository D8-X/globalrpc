# globalrpc
EVM RPC and WSS manager with nonce tracking. HTTPS connections are pooled and reused across calls.

# Usage

## Setup
- define a configuration file as provided in the example below
- Redis needs to be present and accessed via `redisAddr` and `redisPw` (password can be an empty string)
- create an instance:
```go
grpc, err := globalrpc.NewGlobalRpc(chainId, "rpc_config.json", redisAddr, redisPw)
```

## RpcQuery (for read operations)
Retries across different RPC nodes. Return a `NonRetryableError` if retrying won't help.
```go
result, err := globalrpc.RpcQuery(ctx, grpc, 4, 10*time.Second,
    func(ctx context.Context, rpc *ethclient.Client) ([]string, error) {
        return myFunction(poolId, rpc)
    },
)
```

## RpcExec (for write operations)

```go
_, err := globalrpc.RpcExec(ctx, grpc, 3, 2*time.Second,
    func(ctx context.Context, rpc *ethclient.Client, attempt int, prevErr error) (int, error) {
        // globalrpc related errors. Here just for proper logging on the resons of teh retry but can be used to trigger specific logic on certain errors if needed
        var rpcErr *globalrpc.RpcError
        if errors.As(prevErr, &rpcErr) {
            slog.Warn("rpc error, retrying on next node", "attempt", attempt, "kind", rpcErr.Kind, "err", rpcErr)
        }

        // tx error
        var txErr *globalrpc.TxError
        if errors.As(prevErr, &txErr) {
            switch txErr.Kind {
            case globalrpc.TxErrNonceTooLow:
                // reseed from chain then retrying should work if nonce was the issue
                if err := tracker.Seed(ctx, rpc); err != nil {
                    return 0, globalrpc.NewNonRetryableError(err)
                }
            case globalrpc.TxErrNonceTooHigh:
                // blob tx nonce gap, reseed
                if err := tracker.Seed(ctx, rpc); err != nil {
                    return 0, globalrpc.NewNonRetryableError(err)
                }
            case globalrpc.TxErrAlreadyKnown:
                // exact tx already in mempool safe to skip
                return 0, nil
            case globalrpc.TxErrReplaceUnderpriced:
                //  gas too low to replace
                // ..
            case globalrpc.TxErrIntrinsicGas:
                // gas limit below minimum
                // to solve before retrying
            case globalrpc.TxErrFeeCapTooLow:
                // maxFeePerGas below base fee
                // to solve before retrying
            case globalrpc.TxErrInsufficientFunds:
                // wallet can't cover gas + value no point retrying
                return 0, globalrpc.NewNonRetryableError(prevErr)
            case globalrpc.TxErrGasLimit:
                // gas exceeds block limit no point retrying
                return 0, globalrpc.NewNonRetryableError(prevErr)
            case globalrpc.TxErrTxTypeNotSupported:
                // chain doesn't support this tx type so no point retrying
                return 0, globalrpc.NewNonRetryableError(prevErr)
            }
            slog.Warn("exec failed, retrying", "attempt", attempt, "kind", txErr.Kind, "prevErr", prevErr)
        }

        // not infrastructure and  not a known tx error sends back the raw error
        // most likely retrying will likely get the same result so we can skip retrying and return the error right away or decide to retry based on the error
        if prevErr != nil && !errors.As(prevErr, &rpcErr) && !errors.As(prevErr, &txErr) {
            return 0, globalrpc.NewNonRetryableError(prevErr)
        }

        return 0, submitTx(ctx, rpc, baseFeeScale)
    },
)
```

## RpcDial
Pins all calls to the same RPC node. Lock is automatically renewed until cleanup is called.
```go
client, cleanup, err := globalrpc.RpcDial(ctx, grpc, globalrpc.TypeHTTPS)
if err != nil { return err }
defer cleanup()
```

## Nonce Tracking
Redis-backed nonce counter. Seed on startup (always syncs from chain), then call `Next()` for each transaction:
```go
tracker := grpc.NewNonceTracker(address)
tracker.Seed(ctx, client)
nonce, err := tracker.Next()

// on nonce error, resync from chain:
tracker.Seed(ctx, client)
```

# RPC Config File Example

```json
[
    {
        "chainId": 999,
        "https": [
            "https://rpc.hyperliquid.xyz/evm",
            "https://rpc.hypurrscan.io",
            "https://hyperliquid.drpc.org",
            "https://rpc.hyperlend.finance"
        ],
        "wss": [
            "wss://hyperliquid.drpc.org",
            "wss://api.hyperliquid.xyz/ws"
        ]
    },
    {
        "chainId": 989,
        "https": [
            "https://spectrum-01.simplystaking.xyz/hyperliquid-tn-rpc/evm",
            "https://rpc.hyperliquid-testnet.xyz/evm"
        ],
        "wss": []
    }
]
```
