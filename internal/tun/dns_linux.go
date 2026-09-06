//go:build linux

package tun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// dnsApplyTimeout bounds one resolver operation. DNS setup must never stall the
// tunnel: a slow resolvectl is a warning, not a failed Up.
const dnsApplyTimeout = 5 * time.Second

// resolvConfPath is the system resolver file used when systemd-resolved is absent.
const resolvConfPath = "/etc/resolv.conf"

// resolvConfBackupPath keeps the pre-tunnel contents while the tunnel owns DNS.
const resolvConfBackupPath = "/run/sa05/resolv.conf.bak"

// applyDNS points name resolution at the tunnel. systemd-resolved is preferred:
// it scopes the change to the tunnel link, so other links keep working and a
// crash cannot leave the machine without a resolver (revert on Down restores).
// Without resolved the file fallback rewrites /etc/resolv.conf with a backup.
// Both are best-effort: Up succeeds either way, the shortfall lands in message.
func (t *Tunnel) applyDNS(config Config) {
	if tryResolvectl(config.dns()) == nil {
		t.dnsMode = "resolvectl"
		return
	}
	if err := overwriteResolvConf(config.dns()); err == nil {
		t.dnsMode = "resolv.conf"
		return
	}
	t.dnsMode = ""
	t.message = joinMessage(t.message,
		"DNS не настроен: запросы могут идти напрямую")
}

// revertDNS undoes whatever applyDNS did, best-effort.
func (t *Tunnel) revertDNS() {
	switch t.dnsMode {
	case "resolvectl":
		_ = runDNSCmd("resolvectl", "revert", DeviceName)
	case "resolv.conf":
		_ = restoreResolvConf()
	}
	t.dnsMode = ""
}

func tryResolvectl(dns string) error {
	if _, err := exec.LookPath("resolvectl"); err != nil {
		return err
	}
	if err := runDNSCmd("resolvectl", "dns", DeviceName, dns); err != nil {
		return err
	}
	// "~." routes every domain through this link; without it resolved keeps
	// asking the old link first and answers leak around the tunnel.
	if err := runDNSCmd("resolvectl", "domain", DeviceName, "~."); err != nil {
		_ = runDNSCmd("resolvectl", "revert", DeviceName)
		return err
	}
	if err := runDNSCmd("resolvectl", "default-route", DeviceName, "yes"); err != nil {
		_ = runDNSCmd("resolvectl", "revert", DeviceName)
		return err
	}
	return nil
}

// overwriteResolvConf replaces a plain /etc/resolv.conf, keeping a backup.
// A symlink (usually systemd's stub) is refused: replacing the target would
// rewire every link, reverting it would be guesswork.
func overwriteResolvConf(dns string) error {
	info, err := os.Lstat(resolvConfPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s — ссылка, файл не тронут", resolvConfPath)
	}
	previous, err := os.ReadFile(resolvConfPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll("/run/sa05", 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(resolvConfBackupPath, previous, 0o644); err != nil {
		return err
	}
	content := "# SA05: resolver туннеля, исходный — в " + resolvConfBackupPath + "\n" +
		"nameserver " + dns + "\n"
	if err := os.WriteFile(resolvConfPath, []byte(content), 0o644); err != nil {
		return err
	}
	return nil
}

func restoreResolvConf() error {
	previous, err := os.ReadFile(resolvConfBackupPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(resolvConfPath, previous, 0o644); err != nil {
		return err
	}
	_ = os.Remove(resolvConfBackupPath)
	return nil
}

func runDNSCmd(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dnsApplyTimeout)
	defer cancel()
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}

func joinMessage(current, extra string) string {
	if current == "" {
		return extra
	}
	return current + "; " + extra
}
