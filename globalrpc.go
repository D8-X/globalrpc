package globalrpc

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
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
	ruedi    rueidis.Client
	Config   RpcConfig
	pool     *connPool
	log      *slog.Logger
	redisTLS *tls.Config
}

type Option func(*GlobalRpc)

func WithLogger(l *slog.Logger) Option {
	return func(gr *GlobalRpc) {
		if l != nil {
			gr.log = l
		}
	}
}

// WithRedisTLS connects to Redis over TLS using cfg (e.g. &tls.Config{} for
// the system CA pool), as required by AWS ElastiCache with in-transit
// encryption enabled.
func WithRedisTLS(cfg *tls.Config) Option {
	return func(gr *GlobalRpc) {
		gr.redisTLS = cfg
	}
}

// keyTag returns the per chain/rpc-type part of the redis keys, wrapped in
// braces as a Redis Cluster hash tag: all keys of one chain/type (url list,
// round-robin index and url locks) then hash to the same slot, which
// LUA_ACQUIRE requires as it touches all of them (and builds the lock key
// itself).
func keyTag(chain int, rpcType RPCKind) string {
	return "{" + strconv.Itoa(chain) + "_" + rpcType.String() + "}"
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

func NewGlobalRpc(chainId int, configname, redisAddr, redisPw string, opts ...Option) (*GlobalRpc, error) {
	var gr GlobalRpc
	gr.log = slog.Default()
	for _, opt := range opts {
		opt(&gr)
	}
	var err error
	gr.Config, err = loadRPCConfig(chainId, configname)
	if err != nil {
		return nil, err
	}
	client, err := rueidis.NewClient(
		rueidis.ClientOption{
			InitAddress: []string{redisAddr},
			Password:    redisPw,
			TLSConfig:   gr.redisTLS,
			// No client-side caching: nothing here uses it, and turning it
			// on (CLIENT TRACKING) fails the connection on servers without
			// it, e.g. ElastiCache Serverless.
			DisableCache: true,
		})
	if err != nil {
		return nil, err
	}
	gr.ruedi = client
	gr.pool = newConnPool()
	err = urlToRedis(gr.Config.ChainId, TypeHTTPS, gr.Config.Https, client, gr.log)
	if err != nil {
		return nil, err
	}
	err = urlToRedis(gr.Config.ChainId, TypeWSS, gr.Config.Wss, client, gr.log)
	if err != nil {
		return nil, err
	}
	return &gr, nil
}

func urlToRedis(chain int, urlType RPCKind, urls []string, c rueidis.Client, log *slog.Logger) error {
	key := REDIS_SET_URL_LOCK + strconv.Itoa(chain) + urlType.String()
	cmd := c.B().Del().Key(key).Build()
	err := c.Do(context.Background(), cmd).Error()
	if err != nil {
		log.Info("unable to delete existing urls", "chain", chain, "type", urlType.String())
	}
	cmd = c.B().Set().Key(key).Value("locked").Nx().
		Ex(time.Minute * 10).Build()
	err = c.Do(context.Background(), cmd).Error()
	if err != nil {
		log.Info("urls already set", "chain", chain, "type", urlType.String())
		return nil
	}
	if len(urls) == 0 {
		log.Info("no urls provided for", "chain", chain, "rpc type", urlType.String())
		return nil
	}
	key = REDIS_KEY_URLS + keyTag(chain, urlType)
	cmd = c.B().Del().Key(key).Build()
	c.Do(context.Background(), cmd)
	cmd = c.B().Rpush().Key(key).Element(urls...).Build()
	if err := c.Do(context.Background(), cmd).Error(); err != nil {
		return err
	}
	return nil
}

func (gr *GlobalRpc) urlsFor(rpcType RPCKind) []string {
	if rpcType == TypeWSS {
		return gr.Config.Wss
	}
	return gr.Config.Https
}

func (gr *GlobalRpc) GetAndLockRpc(ctx context.Context, rpcType RPCKind, maxWaitSec int) (Receipt, error) {
	c := gr.ruedi
	chainType := keyTag(gr.Config.ChainId, rpcType)
	lockID := randomLockID()
	waitMs := 0
	args := append([]string{EXPIRY_SEC, lockID}, gr.urlsFor(rpcType)...)

	for {
		cmd := c.B().Eval().Script(LUA_ACQUIRE).Numkeys(3).Key(
			REDIS_KEY_CURR_IDX+chainType,
			REDIS_KEY_URLS+chainType,
			REDIS_KEY_LOCK+chainType).Arg(args...).Build()
		url, seeded, err := parseAcquire(c.Do(ctx, cmd))
		if err == nil && url != "" {
			if seeded {
				gr.log.Warn("globalrpc url list was empty, reseeded from config",
					"chain", gr.Config.ChainId, "type", rpcType.String(), "urls", len(gr.urlsFor(rpcType)))
			}
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

func parseAcquire(res rueidis.RedisResult) (string, bool, error) {
	arr, err := res.ToArray()
	if err != nil || len(arr) != 2 {
		return "", false, err
	}
	url, err := arr[0].ToString()
	if err != nil {
		return "", false, err
	}
	seeded, err := arr[1].AsInt64()
	if err != nil {
		return url, false, nil
	}
	return url, seeded == 1, nil
}

func (gr *GlobalRpc) ReturnLock(rec Receipt) {
	if rec.lockID == "" {
		return
	}
	chainType := keyTag(gr.Config.ChainId, rec.RpcType)
	key := REDIS_KEY_LOCK + chainType + rec.Url
	c := gr.ruedi
	cmd := c.B().Eval().Script(LUA_RELEASE).Numkeys(1).Key(key).Arg(rec.lockID).Build()
	err := c.Do(context.Background(), cmd).Error()
	if err != nil {
		gr.log.Error("ReturnLock", "error", err)
	}
}

func (gr *GlobalRpc) renewLock(rec Receipt) {
	chainType := keyTag(gr.Config.ChainId, rec.RpcType)
	key := REDIS_KEY_LOCK + chainType + rec.Url
	c := gr.ruedi
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
		return zero, &RpcError{Kind: RpcErrLock, Err: err}
	}
	defer rpcH.ReturnLock(rec)

	actx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	rpc, err := rpcH.pool.getClient(actx, rec.Url)
	if err != nil {
		return zero, &RpcError{Kind: RpcErrDial, Err: err}
	}

	v, err := call(actx, rpc)
	if err != nil {
		if isConnectionError(err) {
			rpcH.pool.removeClient(rec.Url)
			return zero, &RpcError{Kind: RpcErrConnection, Err: err}
		}
		rpcH.log.Error("rpcAttempt", "error", err)
		return zero, err
	}
	return v, nil
}

func RpcDial(ctx context.Context, rpcH *GlobalRpc, rpcType RPCKind) (*ethclient.Client, func(), string, error) {
	rec, err := rpcH.GetAndLockRpc(ctx, rpcType, 10)
	if err != nil {
		return nil, nil, "", err
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
		return nil, nil, "", err
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
	return rpc, cleanup, rec.Url, nil
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
	for i := range attempts {
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

// RpcExec retries a write operation across RPC nodes. The callback's prevErr
// is either a *TxError (classified tx rejection), a *RpcError (infrastructure),
// or a plain error (unclassified). On the first attempt prevErr is nil.
func RpcExec[T any](
	ctx context.Context,
	rpcH *GlobalRpc,
	attempts int,
	wait time.Duration,
	call func(ctx context.Context, rpc *ethclient.Client, attempt int, prevErr error) (T, error),
) (T, error) {
	var v T
	var prevErr error
	if attempts < 1 {
		return v, fmt.Errorf("attempts must be >= 1")
	}
	for i := range attempts {
		wrapped := func(ctx context.Context, rpc *ethclient.Client) (T, error) {
			return call(ctx, rpc, i, prevErr)
		}
		var err error
		if v, err = rpcAttempt(ctx, rpcH, wait, wrapped); err == nil {
			return v, nil
		}
		if IsNonRetryable(err) {
			return v, err
		}
		var rpcErr *RpcError
		if errors.As(err, &rpcErr) {
			prevErr = err
		} else if kind := ClassifyTxErr(err); kind != TxErrUnknown {
			prevErr = &TxError{Kind: kind, Err: err}
		} else {
			prevErr = err
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
	return v, fmt.Errorf("rpc exec failed after %d attempts: %v", attempts, prevErr)
}
