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
refetchNonce := false // we need to set a tracker

_, err := globalrpc.RpcExec(ctx, grpc, 3, 2*time.Second,
    func(ctx context.Context, rpc *ethclient.Client) (int, error) {

        // we reseed nonce from chain if the onError has flagged it
        if refetchNonce {
            if err := tracker.Seed(ctx, rpc); err != nil {
                return 0, globalrpc.NewNonRetryableError(err)
            }
            refetchNonce = false
        }
        opts := d8x_futures.OptsOverridesExec{
            OptsOverrides: d8x_futures.OptsOverrides{
                WalletIdx: walletIdx,
                Rpc:       rpc,
            },
            TsMin:      tsMin,
            PayoutAddr: treasury.Address,
        }
        return 0, execOrder(orderIds, sym, &opts, baseFeeScale)
    },
    func(err error, attempt int) error {
        // no point retrying here
        if strings.Contains(err.Error(), "insufficient funds") {
            slog.Info("wallet needs refill", "wallet", walletAddr.Hex())
            return globalrpc.NewNonRetryableError(err)
        }
        // same here
        if strings.Contains(err.Error(), "gas required exceeds allowance") {
            return globalrpc.NewNonRetryableError(err)
        }
        // here we can adjust before retrying
        if strings.Contains(err.Error(), "intrinsic gas too low") {
            baseFeeScale = baseFeeScale * 2
        }
        // --> we set a flag here to ask refercting nonce on next attemp
        if strings.Contains(err.Error(), "nonce too low") {
            refetchNonce = true
        }
        slog.Warn("exec failed, retrying", "attempt", attempt, "error", err)
        return err
    },
)
if err == nil {
    slog.Info("order executed ✓")
} else {
    slog.Error("execution failed", "error", err)
}
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
