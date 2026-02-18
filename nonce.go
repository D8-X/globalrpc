package globalrpc

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/redis/rueidis"
)

const REDIS_KEY_NONCE = "glbl_rpc:nonce:"

type NonceProvider interface {
	Seed(ctx context.Context, client *ethclient.Client) error
	Next() (uint64, error)
}

type nonceTracker struct {
	address common.Address
	chainID int
	redis   *rueidis.Client
}

func (t *nonceTracker) redisKey() string {
	return REDIS_KEY_NONCE + strconv.Itoa(t.chainID) + ":" + t.address.Hex()
}

func (t *nonceTracker) Seed(ctx context.Context, client *ethclient.Client) error {
	n, err := client.PendingNonceAt(ctx, t.address)
	if err != nil {
		return err
	}
	c := *t.redis
	key := t.redisKey()
	cmd := c.B().Set().Key(key).Value(strconv.FormatUint(n, 10)).Build()
	return c.Do(ctx, cmd).Error()
}

func (t *nonceTracker) Next() (uint64, error) {
	c := *t.redis
	key := t.redisKey()
	cmd := c.B().Eval().Script(LUA_NONCE_NEXT).Numkeys(1).Key(key).Build()
	val, err := c.Do(context.Background(), cmd).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("redis nonce next: %w", err)
	}
	return uint64(val) - 1, nil
}

func (gr *GlobalRpc) NewNonceTracker(address common.Address) NonceProvider {
	return &nonceTracker{address: address, chainID: gr.Config.ChainId, redis: gr.ruedi}
}
