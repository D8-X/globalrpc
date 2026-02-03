# global-rpc
EVM RPC and WSS manager

# Usage
- define a configuration file as provided in the example below
- REDIS needs to be present and accessed via "redisAddr" and "redisPW" (password can be an empty string)
- create an instance `glblRpc, err = globalrpc.NewGlobalRpc(int(chainConfig.ChainId), rpcConf, redisAddr, redisPw)`
- most convieniently is to call RpcQuery, for example
```
    liquidatableInPool, err := globalrpc.RpcQuery(context.Background(), lq.glblRpc, 4, 10*time.Second,
        func(ctx context.Context, rpc *ethclient.Client) ([]string, error) {
            return myFunction(poolId, rpc)// myFunction returns a []string and error 
        },
    )
```
- return a `NonRetryableError` in your function if you know that with this error it won't make sense to retry

# RPC Config File Example

```
[
    {
        "chainId": 999,
        "https": [
            "https://rpc.hyperliquid.xyz/evm",
            "https://rpc.hypurrscan.io",
            "https://hyperliquid.drpc.org",
            "https://rpc.hyperlend.finance",
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