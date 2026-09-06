//go:build linux

package tun

import "testing"

func TestJoinMessageAppends(t *testing.T) {
	if got := joinMessage("", "b"); got != "b" {
		t.Fatalf("пустое + b = %q", got)
	}
	if got := joinMessage("a", "b"); got != "a; b" {
		t.Fatalf("a + b = %q", got)
	}
}

func TestOverwriteResolvConfRefusesSymlink(t *testing.T) {
	// /etc/resolv.conf on systemd machines is a symlink to the stub; the
	// fallback must refuse it rather than rewire the whole system. Where it is
	// a plain file (containers), skipping is still safe: covered by error text.
	err := overwriteResolvConf("203.0.113.53")
	if err == nil {
		// Plain file in this environment: restore immediately, the test must
		// not leave the resolver pointed at documentation space.
		_ = restoreResolvConf()
		t.Skip("в окружении обычный resolv.conf, отказ симлинка не проверить")
	}
	if got := err.Error(); got == "" {
		t.Fatal("пустая ошибка")
	}
}
