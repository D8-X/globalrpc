package globalrpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
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
	pool   *connPool
}

type Receipt struct {
	Url     string
	RpcType RPCKind
	lockID  string
}

func randomLockID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func NewGlobalRpc(chainId int, configname, redisAddr, redisPw string) (*GlobalRpc, error) {
	var gr GlobalRpc
	var err error
	gr.Config, err = loadRPCConfig(chainId, configname)
	if err != nil {
		return nil, err
	}
	client, err := rueidis.NewClient(
		rueidis.ClientOption{InitAddress: []string{redisAddr}, Password: redisPw})
	if err != nil {
		return nil, err
	}
	gr.ruedi = &client
	gr.pool = newConnPool()
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

func urlToRedis(chain int, urlType RPCKind, urls []string, client *rueidis.Client) error {
	c := *client
	key := REDIS_SET_URL_LOCK + strconv.Itoa(chain) + urlType.String()
	cmd := c.B().Del().Key(key).Build()
	err := c.Do(context.Background(), cmd).Error()
	if err != nil {
		slog.Info("unable to delete existing urls", "chain", chain, "type", urlType.String())
	}
	cmd = c.B().Set().Key(key).Value("locked").Nx().
		Ex(time.Minute * 10).Build()
	err = c.Do(context.Background(), cmd).Error()
	if err != nil {
		slog.Info("urls already set", "chain", chain, "type", urlType.String())
		return nil
	}
	if len(urls) == 0 {
		slog.Info("no urls provided for", "rpc type", urlType.String())
		return nil
	}
	key = REDIS_KEY_URLS + strconv.Itoa(chain) + "_" + urlType.String()
	cmd = c.B().Del().Key(key).Build()
	c.Do(context.Background(), cmd)
	cmd = c.B().Rpush().Key(key).Element(urls...).Build()
	if err := c.Do(context.Background(), cmd).Error(); err != nil {
		return err
	}
	return nil
}

func (gr *GlobalRpc) GetAndLockRpc(ctx context.Context, rpcType RPCKind, maxWaitSec int) (Receipt, error) {
	c := *gr.ruedi
	chainType := strconv.Itoa(gr.Config.ChainId) + "_" + rpcType.String()
	lockID := randomLockID()
	waitMs := 0

	for {
		cmd := c.B().Eval().Script(LUA_ACQUIRE).Numkeys(3).Key(
			REDIS_KEY_CURR_IDX+chainType,
			REDIS_KEY_URLS+chainType,
			REDIS_KEY_LOCK+chainType).Arg(EXPIRY_SEC, lockID).Build()
		url, err := c.Do(ctx, cmd).ToString()
		if err == nil && url != "" {
			return Receipt{Url: url, RpcType: rpcType, lockID: lockID}, nil
		}
		select {
		case <-ctx.Done():
			return Receipt{}, ctx.Err()
		default:
		}
		time.Sleep(250 * time.Millisecond)
		waitMs += 250
		if waitMs > maxWaitSec*1000 {
			return Receipt{}, fmt.Errorf("unable to get rpc")
		}
	}
}

func (gr *GlobalRpc) ReturnLock(rec Receipt) {
	if rec.lockID == "" {
		return
	}
	chainType := strconv.Itoa(gr.Config.ChainId) + "_" + rec.RpcType.String()
	key := REDIS_KEY_LOCK + chainType + rec.Url
	c := *gr.ruedi
	cmd := c.B().Eval().Script(LUA_RELEASE).Numkeys(1).Key(key).Arg(rec.lockID).Build()
	err := c.Do(context.Background(), cmd).Error()
	if err != nil {
		slog.Error("ReturnLock", "error", err)
	}
}

func (gr *GlobalRpc) renewLock(rec Receipt) {
	chainType := strconv.Itoa(gr.Config.ChainId) + "_" + rec.RpcType.String()
	key := REDIS_KEY_LOCK + chainType + rec.Url
	c := *gr.ruedi
	cmd := c.B().Eval().Script(LUA_RENEW).Numkeys(1).Key(key).Arg(rec.lockID, EXPIRY_SEC).Build()
	c.Do(context.Background(), cmd)
}

func (gr *GlobalRpc) renewLoop(rec Receipt, stop chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			gr.renewLock(rec)
		}
	}
}

func rpcAttempt[T any](
	ctx context.Context,
	rpcH *GlobalRpc,
	wait time.Duration,
	call func(ctx context.Context, rpc *ethclient.Client) (T, error),
) (T, error) {
	var zero T

	rec, err := rpcH.GetAndLockRpc(ctx, TypeHTTPS, int(wait.Seconds()))
	if err != nil {
		return zero, err
	}
	defer rpcH.ReturnLock(rec)

	actx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	rpc, err := rpcH.pool.getClient(actx, rec.Url)
	if err != nil {
		return zero, err
	}

	v, err := call(actx, rpc)
	if err != nil {
		slog.Error("rpcAttempt", "error", err)
		return zero, err
	}
	return v, nil
}

func RpcDial(ctx context.Context, rpcH *GlobalRpc, rpcType RPCKind) (*ethclient.Client, func(), error) {
	rec, err := rpcH.GetAndLockRpc(ctx, rpcType, 10)
	if err != nil {
		return nil, nil, err
	}

	var rpc *ethclient.Client
	pooled := isHTTPS(rec.Url)
	if pooled {
		rpc, err = rpcH.pool.getClient(ctx, rec.Url)
	} else {
		rpc, err = ethclient.DialContext(ctx, rec.Url)
	}
	if err != nil {
		rpcH.ReturnLock(rec)
		return nil, nil, err
	}

	stop := make(chan struct{})
	go rpcH.renewLoop(rec, stop)

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			close(stop)
			if !pooled {
				rpc.Close()
			}
			rpcH.ReturnLock(rec)
		})
	}
	return rpc, cleanup, nil
}

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
			return v, err
		}
		if IsNonRetryable(err) {
			return v, err
		}
		if i+1 < attempts {
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
