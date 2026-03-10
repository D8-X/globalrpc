package globalrpc

import (
	"context"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/ethclient"
)

type connPool struct {
	mu      sync.Mutex
	clients map[string]*ethclient.Client
}

func newConnPool() *connPool {
	return &connPool{
		clients: make(map[string]*ethclient.Client),
	}
}

func (p *connPool) getClient(ctx context.Context, url string) (*ethclient.Client, error) {
	p.mu.Lock()
	if c, ok := p.clients[url]; ok {
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()

	c, err := ethclient.DialContext(ctx, url)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()

	if existing, ok := p.clients[url]; ok {
		p.mu.Unlock()
		c.Close()
		return existing, nil
	}
	p.clients[url] = c
	p.mu.Unlock()
	return c, nil
}

func isHTTPS(url string) bool {
	return strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://")
}

func (p *connPool) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for url, c := range p.clients {
		c.Close()
		delete(p.clients, url)
	}
}
