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

## RpcDial (for write operations)
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
