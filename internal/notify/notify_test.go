package notify

import (
	"testing"
	"time"
)

func TestAdmitSuppressesRepeatsInsideWindow(t *testing.T) {
	notifier := New()
	now := time.Now()
	if !notifier.admit("заголовок\x00тело", now) {
		t.Fatal("первое сообщение не прошло фильтр")
	}
	if notifier.admit("заголовок\x00тело", now.Add(repeatWindow-time.Second)) {
		t.Fatal("повтор внутри окна не подавлен")
	}
	if !notifier.admit("заголовок\x00другое тело", now) {
		t.Fatal("другой текст подавлен")
	}
	if !notifier.admit("заголовок\x00тело", now.Add(repeatWindow+time.Second)) {
		t.Fatal("сообщение после окна не прошло фильтр")
	}
}
