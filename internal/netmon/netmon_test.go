package netmon

import (
	"context"
	"testing"
	"time"
)

func TestCurrentIsStableAndNonEmptyOnline(t *testing.T) {
	first := Current()
	if first == None {
		t.Skip("нет активной сети")
	}
	if Current() != first {
		t.Fatal("отпечаток сети нестабилен между вызовами")
	}
	if !Online() {
		t.Fatal("сеть есть, но Online() == false")
	}
}

func TestWatchStartsAndCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher, err := Watch(ctx)
	if err != nil {
		t.Skipf("наблюдение недоступно: %v", err)
	}
	watcher.Close()
	watcher.Close()
	select {
	case <-watcher.Changes():
	case <-time.After(2 * time.Second):
		t.Fatal("канал не закрылся после Close")
	}
}
