package globalrpc

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/redis/rueidis"
)

const (
	EXPIRY_SEC         = "10" // rpc locks expire after this amount of time
	REDIS_KEY_CURR_IDX = "glbl_rpc:idx"
	REDIS_KEY_URLS     = "glbl_rpc:urls"
	REDIS_KEY_LOCK     = "glbl_rpc:lock"
	REDIS_SET_URL_LOCK = "glbl_rpc:urls_lock"
)

type GlobalRpc struct {
	ruedi  *rueidis.Client
	Config RpcConfig
}

type Receipt struct {
	Url     string
	RpcType RPCKind
}

// NewGlobalRpc initializes a new RPC handler for the given config-filename and
// redis credentials
func NewGlobalRpc(chainId int, configname, redisAddr, redisPw string) (*GlobalRpc, error) {
	var gr GlobalRpc
	var err error
	gr.Config, err = loadRPCConfig(chainId, configname)
	if err != nil {
		return nil, err
	}
	// ruedis client
	client, err := rueidis.NewClient(
		rueidis.ClientOption{InitAddress: []string{redisAddr}, Password: redisPw})
	if err != nil {
		return nil, err
	}
	// store URLs to Redis
	gr.ruedi = &client
	err = urlToRedis(gr.Config.ChainId, TypeHTTPS, gr.Config.Https, &client)
	if err != nil {
		return nil, err
	}
	err = urlToRedis(gr.Config.ChainId, TypeWSS, gr.Config.Wss, &client)
	if err != nil {
		return nil, err
	}
	return &gr, nil
}

// urlToRedis sets the urls for the given type and chain unless set recently
func urlToRedis(chain int, urlType RPCKind, urls []string, client *rueidis.Client) error {
	c := *client
	// check whether anyone has set the urls recently
	key := REDIS_SET_URL_LOCK + strconv.Itoa(chain) + urlType.String()
	// delete first
	cmd := c.B().Del().Key(key).Build()
	err := c.Do(context.Background(), cmd).Error()
	if err != nil {
		slog.Info("unable to delete existing urls", "chain", chain, "type", urlType.String())
	}
	// now add
	cmd = c.B().Set().Key(key).Value("locked").Nx().
		Ex(time.Minute * 10).Build()
	err = c.Do(context.Background(), cmd).Error()
	if err != nil {
		slog.Info("urls already set", "chain", chain, "type", urlType.String())
		return nil
	}
	// push urls
	if len(urls) == 0 {
		slog.Info("no urls provided for", "rpc type", urlType.String())
		return nil
	}
	key = REDIS_KEY_URLS + strconv.Itoa(chain) + "_" + urlType.String()
	// Delete existing URLs first to prevent duplication
	cmd = c.B().Del().Key(key).Build()
	c.Do(context.Background(), cmd)
	// push new url
	cmd = c.B().Rpush().Key(key).Element(urls...).Build()
	if err := c.Do(context.Background(), cmd).Error(); err != nil {
		return err
	}
	return nil
}

// GetAndLockRpc returns a receipt
func (gr *GlobalRpc) GetAndLockRpc(rpcType RPCKind, maxWaitSec int) (Receipt, error) {
	c := *gr.ruedi
	chainType := strconv.Itoa(gr.Config.ChainId) + "_" + rpcType.String()
	waitMs := 0

	var url string
	for {
		var err error
		cmd := c.B().Eval().Script(LUA_SCRIPT).Numkeys(3).Key(
			REDIS_KEY_CURR_IDX+chainType,
			REDIS_KEY_URLS+chainType,
			REDIS_KEY_LOCK+chainType).Arg(EXPIRY_SEC).Build()
		url, err = c.Do(context.Background(), cmd).ToString()
		if err != nil || url == "" {
			slog.Info("no available url", "chain_type", chainType)
			time.Sleep(250 * time.Millisecond)
			waitMs += 250
			if waitMs > maxWaitSec*1000 {
				return Receipt{}, fmt.Errorf("unable to get rpc")
			}
			continue
		}
		break
	}
	return Receipt{Url: url, RpcType: rpcType}, nil
}

// ReturnLock is for the user to return the receipt, so other
// instances can get the url. If the receipt is not returned,
// the lock expires after 60 seconds
func (gr *GlobalRpc) ReturnLock(rec Receipt) {
	chainType := strconv.Itoa(gr.Config.ChainId) + "_" + rec.RpcType.String()
	key := REDIS_KEY_LOCK + chainType + rec.Url
	c := *gr.ruedi
	cmd := c.B().Del().Key(key).Build()
	err := c.Do(context.Background(), cmd).Error()
	if err != nil {
		fmt.Println("error", err.Error())
	}
}

// One attempt, with proper locking/closing and per-attempt timeout.
func rpcAttempt[T any](
	ctx context.Context,
	rpcH *GlobalRpc,
	wait time.Duration,
	call func(ctx context.Context, rpc *ethclient.Client) (T, error),
) (T, error) {
	var zero T

	rec, err := rpcH.GetAndLockRpc(TypeHTTPS, int(wait.Seconds()))
	if err != nil {
		return zero, err
	}
	defer rpcH.ReturnLock(rec)

	actx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	rpc, err := ethclient.DialContext(actx, rec.Url)
	if err != nil {
		return zero, err
	}
	defer rpc.Close()

	v, err := call(actx, rpc)
	if err != nil {
		slog.Error("rpcAttempt", "error", err)
		return zero, err
	}
	return v, nil
}

// RpcQuery tries to execute a function that requires an rpc
// and retries
func RpcQuery[T any](
	ctx context.Context,
	rpcH *GlobalRpc,
	attempts int,
	wait time.Duration,
	call func(ctx context.Context, rpc *ethclient.Client) (T, error),
) (T, error) {
	var v T
	var err error
	if attempts < 1 {
		return v, fmt.Errorf("attempts must be >= 1")
	}
	for i := 0; i < attempts; i++ {
		if v, err = rpcAttempt(ctx, rpcH, wait, call); err == nil {
			// done
			return v, err
		}
		if IsNonRetryable(err) {
			return v, err
		}
		if i+1 < attempts {
			// simple backoff with context-aware sleep
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return v, ctx.Err()
			case <-t.C:
			}
		}
	}
	return v, fmt.Errorf("rpc query failed after %d attempts: %v", attempts, err)
}
