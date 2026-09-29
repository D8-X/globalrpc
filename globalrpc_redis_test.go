package globalrpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// TestRedis exercises every redis code path (url seeding, lock acquire,
// renew and release, nonce tracking) against the redis at
// GLOBALRPC_TEST_REDIS, e.g. a node of a local Redis Cluster to verify all
// keys a script touches share one slot. Skipped when unset.
func TestRedis(t *testing.T) {
	addr := os.Getenv("GLOBALRPC_TEST_REDIS")
	if addr == "" {
		t.Skip("GLOBALRPC_TEST_REDIS not set")
	}
	cfg := filepath.Join(t.TempDir(), "rpc.json")
	err := os.WriteFile(cfg, []byte(`[{"chainId": 31337,
		"https": ["https://a.example", "https://b.example"],
		"wss": ["wss://a.example"]}]`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	gr, err := NewGlobalRpc(31337, cfg, addr, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// both https urls can be locked at once, a third acquire must wait
	r1, err := gr.GetAndLockRpc(ctx, TypeHTTPS, 1)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := gr.GetAndLockRpc(ctx, TypeHTTPS, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Url == r2.Url {
		t.Fatalf("same url locked twice: %s", r1.Url)
	}
	if _, err := gr.GetAndLockRpc(ctx, TypeHTTPS, 0); err == nil {
		t.Fatal("expected no free url")
	}
	gr.renewLock(r1)
	gr.ReturnLock(r1)
	gr.ReturnLock(r2)
	if _, err := gr.GetAndLockRpc(ctx, TypeHTTPS, 1); err != nil {
		t.Fatalf("url not released: %v", err)
	}

	// an emptied url list is reseeded from config by LUA_ACQUIRE
	c := *gr.ruedi
	key := REDIS_KEY_URLS + keyTag(31337, TypeWSS)
	if err := c.Do(ctx, c.B().Del().Key(key).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	rw, err := gr.GetAndLockRpc(ctx, TypeWSS, 1)
	if err != nil || rw.Url != "wss://a.example" {
		t.Fatalf("reseed: url %q, err %v", rw.Url, err)
	}
	gr.ReturnLock(rw)

	nt := gr.NewNonceTracker(common.HexToAddress("0x01")).(*nonceTracker)
	if err := c.Do(ctx, c.B().Set().Key(nt.redisKey()).Value("7").Build()).Error(); err != nil {
		t.Fatal(err)
	}
	if n, err := nt.Next(); err != nil || n != 7 {
		t.Fatalf("nonce: got %d, err %v", n, err)
	}
}
