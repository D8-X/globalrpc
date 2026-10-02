package globalrpc

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	neturl "net/url"
	"slices"
	"strconv"
	"strings"
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

type triedNodes struct {
	seen map[string]bool
	last string
}

func newTriedNodes() *triedNodes {
	return &triedNodes{seen: make(map[string]bool)}
}

func (t *triedNodes) mark(url string) {
	t.seen[url] = true
	t.last = url
}

func (gr *GlobalRpc) lockUntried(ctx context.Context, maxWaitSec int, tried *triedNodes) (Receipt, error) {
	rec, err := gr.GetAndLockRpc(ctx, TypeHTTPS, maxWaitSec)
	if err != nil || !tried.seen[rec.Url] {
		return rec, err
	}
	urls := gr.redisUrls(ctx, rec.RpcType)
	if !slices.ContainsFunc(urls, func(u string) bool { return !tried.seen[u] }) {
		clear(tried.seen)
		tried.seen[tried.last] = true
		if rec.Url != tried.last {
			return rec, nil
		}
	}
	from := slices.Index(urls, rec.Url) - 1
	next, ok := gr.lockFirst(ctx, rec.RpcType, urls, from, func(u string) bool { return !tried.seen[u] })
	if !ok && rec.Url == tried.last && len(tried.seen) > 1 {
		next, ok = gr.lockFirst(ctx, rec.RpcType, urls, from, func(u string) bool { return tried.seen[u] && u != tried.last })
	}
	if !ok {
		return rec, nil
	}
	gr.ReturnLock(rec)
	return next, nil
}

func (gr *GlobalRpc) lockFirst(ctx context.Context, rpcType RPCKind, urls []string, from int, want func(string) bool) (Receipt, bool) {
	c := *gr.ruedi
	chainType := strconv.Itoa(gr.Config.ChainId) + "_" + rpcType.String()
	n := len(urls)
	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer scancel()
	for k := range urls {
		if ctx.Err() != nil || sctx.Err() != nil {
			break
		}
		u := urls[((from-k)%n+n)%n]
		if !want(u) {
			continue
		}
		lockID := randomLockID()
		cmd := c.B().Arbitrary("SET").Keys(REDIS_KEY_LOCK+chainType+u).Args(lockID, "NX", "EX", EXPIRY_SEC).Build()
		if c.Do(sctx, cmd).Error() == nil {
			return Receipt{Url: u, RpcType: rpcType, lockID: lockID}, true
		}
	}
	return Receipt{}, false
}

func (gr *GlobalRpc) redisUrls(ctx context.Context, rpcType RPCKind) []string {
	c := *gr.ruedi
	chainType := strconv.Itoa(gr.Config.ChainId) + "_" + rpcType.String()
	urls, err := c.Do(ctx, c.B().Lrange().Key(REDIS_KEY_URLS+chainType).Start(0).Stop(-1).Build()).AsStrSlice()
	if err != nil || len(urls) == 0 {
		return gr.urlsFor(rpcType)
	}
	return urls
}

func nodeHost(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil || u.Host == "" {
		return "unparsable"
	}
	return u.Scheme + "://" + u.Host
}

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.err }

func redactUrl(raw string, err error) error {
	if err == nil || raw == "" {
		return err
	}
	host := nodeHost(raw)
	msg := strings.ReplaceAll(err.Error(), raw, host)
	if trimmed := strings.TrimSuffix(raw, "/"); trimmed != raw {
		msg = strings.ReplaceAll(msg, trimmed, host)
	}
	msg = strings.ReplaceAll(msg, strings.ReplaceAll(raw, "/", `\/`), host)
	if msg == err.Error() {
		return err
	}
	return &redactedError{msg: msg, err: err}
}

func rpcAttempt[T any](
	ctx context.Context,
	rpcH *GlobalRpc,
	wait time.Duration,
	tried *triedNodes,
	call func(ctx context.Context, rpc *ethclient.Client) (T, error),
) (T, error) {
	var zero T

	rec, err := rpcH.lockUntried(ctx, int(wait.Seconds()), tried)
	if err != nil {
		return zero, &RpcError{Kind: RpcErrLock, Err: err}
	}
	defer rpcH.ReturnLock(rec)
	tried.mark(rec.Url)

	stop := make(chan struct{})
	defer close(stop)
	go rpcH.renewLoop(rec, stop)

	actx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	rpc, err := rpcH.pool.getClient(actx, rec.Url)
	if err != nil {
		return zero, &RpcError{Kind: RpcErrDial, Err: redactUrl(rec.Url, err)}
	}

	v, err := call(actx, rpc)
	if err != nil {
		err = redactUrl(rec.Url, err)
		if isConnectionError(err) {
			rpcH.pool.removeClient(rec.Url)
			return zero, &RpcError{Kind: RpcErrConnection, Err: err}
		}
		if kind, ok := classifyRpcErr(err); ok && !errors.Is(ctx.Err(), context.Canceled) {
			rpcH.log.Warn("rpcAttempt retryable node error", "node", nodeHost(rec.Url), "kind", kind.String(), "error", err)
			return zero, &RpcError{Kind: kind, Err: err}
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
	tried := newTriedNodes()
	for i := range attempts {
		if v, err = rpcAttempt(ctx, rpcH, wait, tried, call); err == nil {
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
				return v, fmt.Errorf("%w: %w", ctx.Err(), err)
			case <-t.C:
			}
		}
	}
	return v, fmt.Errorf("rpc query failed after %d attempts: %w", attempts, err)
}

// RpcExec retries a write operation across RPC nodes. The callback's prevErr
// is either a *TxError (classified tx rejection), a *RpcError (infrastructure;
// only RpcErrLock and RpcErrDial guarantee no node saw the write; any other
// Kind, RpcErrTimeout and RpcErrConnection in particular, may mean it was
// applied), or a plain error (unclassified). On the first attempt prevErr is
// nil.
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
	tried := newTriedNodes()
	var unsafe, rejected error
	for i := range attempts {
		wrapped := func(ctx context.Context, rpc *ethclient.Client) (T, error) {
			return call(ctx, rpc, i, execResult(prevErr, unsafe, rejected))
		}
		var err error
		if v, err = rpcAttempt(ctx, rpcH, wait, tried, wrapped); err == nil {
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
		switch {
		case sentNothing(prevErr):
		case provablyRejected(prevErr):
			if rejected == nil {
				rejected = prevErr
			}
		case unsafe == nil:
			unsafe = prevErr
		}
		if i+1 < attempts {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return v, fmt.Errorf("%w: %w", ctx.Err(), execResult(prevErr, unsafe, rejected))
			case <-t.C:
			}
		}
	}
	return v, fmt.Errorf("rpc exec failed after %d attempts: %w", attempts, execResult(prevErr, unsafe, rejected))
}

func execResult(prevErr, unsafe, rejected error) error {
	if sentNothing(prevErr) {
		if unsafe != nil {
			return unsafe
		}
		if rejected != nil {
			return rejected
		}
	}
	return prevErr
}

func sentNothing(err error) bool {
	var rpcErr *RpcError
	if !errors.As(err, &rpcErr) {
		return false
	}
	return rpcErr.Kind == RpcErrLock || rpcErr.Kind == RpcErrDial
}

func provablyRejected(err error) bool {
	var txErr *TxError
	return errors.As(err, &txErr)
}
