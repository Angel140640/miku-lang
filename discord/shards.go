package discord

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RunShards sobe `count` shards (0 = quantos o Discord recomendar) e bloqueia até
// o contexto acabar ou algum shard falhar de forma fatal.
func RunShards(ctx context.Context, rest *Client, newGateway func() *Gateway, count int, logf func(string, ...any)) error {
	url := GatewayURL
	if count <= 0 {
		info, err := rest.Get("/gateway/bot")
		if err != nil {
			return fmt.Errorf("não consegui perguntar ao Discord quantos shards usar: %w", err)
		}
		m, _ := info.(map[string]any)
		n, _ := m["shards"].(float64)
		count = max(int(n), 1)
		if u, ok := m["url"].(string); ok && u != "" {
			url = u
		}
	}
	logf("iniciando %d shard(s)", count)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		gw := newGateway()
		gw.ShardID, gw.ShardCount, gw.URL = i, count, url
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := gw.Run(ctx); err != nil {
				errs <- err
				cancel()
			}
		}()
		if i < count-1 { // o Discord só deixa 1 identify a cada ~5s por bucket
			select {
			case <-gw.Ready():
			case <-time.After(30 * time.Second):
			case <-ctx.Done():
			}
			select {
			case <-time.After(5500 * time.Millisecond):
			case <-ctx.Done():
			}
		}
	}
	wg.Wait()
	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}
