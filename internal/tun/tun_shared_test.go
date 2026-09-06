package tun

import (
	"context"
	"testing"
)

func TestNormalizedBypassIPsDedupsAndTrims(t *testing.T) {
	config := Config{BypassIPs: []string{
		" 1.2.3.4 ",
		"1.2.3.4",
		"Example.COM",
		"example.com",
		"",
		"   ",
		"2001:db8::1",
	}}
	got := config.normalizedBypassIPs()
	want := []string{"1.2.3.4", "Example.COM", "2001:db8::1"}
	if len(got) != len(want) {
		t.Fatalf("получено %q, ожидалось %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("получено %q, ожидалось %q", got, want)
		}
	}
}

func TestNormalizedBypassIPsCapsCount(t *testing.T) {
	entries := make([]string, 0, maxBypassIPs+10)
	for index := 0; index < maxBypassIPs+10; index++ {
		entries = append(entries, "10.0.0."+itoa(index%250+1)+"."+itoa(index/250))
	}
	config := Config{BypassIPs: entries}
	if got := len(config.normalizedBypassIPs()); got != maxBypassIPs {
		t.Fatalf("записей %d, ожидалось %d", got, maxBypassIPs)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func TestResolveBypassIPsKeepsLiteralsSkipsGarbage(t *testing.T) {
	ctx := context.Background()
	got := resolveBypassIPs(ctx, []string{
		"1.2.3.4",
		"1.2.3.4",
		"2001:db8::1",
		"127.0.0.1", // loopback is useless as a route target
		"224.0.0.1", // multicast too
		"nonexistent.invalid",
	})
	seen := map[string]bool{}
	for _, address := range got {
		seen[address.String()] = true
	}
	if !seen["1.2.3.4"] || !seen["2001:db8::1"] {
		t.Fatalf("литералы потеряны: %v", got)
	}
	if seen["127.0.0.1"] || seen["224.0.0.1"] {
		t.Fatalf("маршрутно-бесполезные адреса прошли: %v", got)
	}
}
