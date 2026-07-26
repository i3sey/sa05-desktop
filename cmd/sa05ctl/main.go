// Command sa05ctl drives the SA05 core from a terminal.
//
// It exists so the engine, the subscription contract and the probes can be exercised
// (and debugged) without the GUI, on a machine with no desktop session at all.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/proxy"

	"github.com/fife/sa05-desktop/internal/assets"
	"github.com/fife/sa05-desktop/internal/core/engine"
	"github.com/fife/sa05-desktop/internal/core/ping"
	"github.com/fife/sa05-desktop/internal/core/subscription"
	"github.com/fife/sa05-desktop/internal/core/xrayconf"
	"github.com/fife/sa05-desktop/internal/ipc"
	"github.com/fife/sa05-desktop/internal/netbypass"
	"github.com/fife/sa05-desktop/internal/storage"
	"github.com/fife/sa05-desktop/internal/tgws"
)

const usage = `sa05ctl — отладочный клиент ядра SA05

  sa05ctl import <https-url>   импортировать подписку
  sa05ctl profiles             показать профили
  sa05ctl use <id|номер>       выбрать активный профиль
  sa05ctl up [id|номер] [--socks порт] [--http порт]
                               поднять ядро и держать до Ctrl-C
  sa05ctl status               показать сохранённое состояние
  sa05ctl check <порт> [url]   запрос через SOCKS-порт ядра
  sa05ctl ping                 замерить задержку всех профилей
  sa05ctl tg-link              ссылка для настройки Telegram
  sa05ctl tg-run [транспорт]   поднять Telegram-прокси до Ctrl-C (auto|cf|ws|tcp)
  sa05ctl tun <status|up|down> управление туннелем через системный компонент
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "ошибка: %v\n", err)
		os.Exit(1)
	}
}

func run(command string, args []string) error {
	store, err := storage.Open("")
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch command {
	case "import":
		return importSubscription(ctx, store, args)
	case "profiles":
		return listProfiles(store)
	case "use":
		return useProfile(store, args)
	case "up":
		return up(ctx, store, args)
	case "status":
		return status(store)
	case "check":
		return check(ctx, args)
	case "ping":
		return pingProfiles(ctx, store)
	case "tg-link":
		return telegramLink(store)
	case "tg-run":
		return telegramRun(ctx, store, args)
	case "tun":
		return tunCommand(ctx, args)
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("неизвестная команда %q", command)
	}
}

func importSubscription(ctx context.Context, store *storage.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("нужен один аргумент: ссылка подписки")
	}
	url := args[0]
	if deepLink := subscription.ParseDeepLink(url); deepLink != "" {
		url = deepLink
	}
	state, err := store.Load()
	if err != nil {
		return err
	}
	result, err := subscription.NewClient().Update(ctx, url, state.Subscription)
	if err != nil {
		// The cached subscription is intentionally left untouched on failure.
		return err
	}
	if _, err := store.Update(func(target *storage.State) {
		target.Subscription = result.State
	}); err != nil {
		return err
	}
	if result.NotModified {
		fmt.Printf("Подписка без изменений: %d профилей\n", len(result.State.Profiles))
		return nil
	}
	fmt.Printf("Импортировано профилей: %d\n", len(result.State.Profiles))
	return listProfiles(store)
}

func listProfiles(store *storage.Store) error {
	state, err := store.Load()
	if err != nil {
		return err
	}
	if len(state.Subscription.Profiles) == 0 {
		return errors.New("подписка не импортирована")
	}
	active := state.Subscription.ActiveProfile()
	for index, profile := range state.Subscription.Profiles {
		marker := " "
		if active != nil && active.ID == profile.ID {
			marker = "*"
		}
		remark := subscription.ParseServerRemark(profile.Remarks)
		name := remark.Name
		if remark.Flag != "" {
			name = remark.Flag + " " + name
		}
		fmt.Printf("%s %2d  %s  %s\n", marker, index+1, profile.ID, name)
	}
	return nil
}

func useProfile(store *storage.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("нужен один аргумент: id или номер профиля")
	}
	state, err := store.Load()
	if err != nil {
		return err
	}
	profile, err := resolveProfile(state.Subscription, args[0])
	if err != nil {
		return err
	}
	if _, err := store.Update(func(target *storage.State) {
		target.Subscription.ActiveProfileID = profile.ID
	}); err != nil {
		return err
	}
	fmt.Printf("Активный профиль: %s\n", profile.Remarks)
	return nil
}

func up(ctx context.Context, store *storage.Store, args []string) error {
	state, err := store.Load()
	if err != nil {
		return err
	}
	if !state.Subscription.Authorized() {
		return errors.New("нет действующей подписки: сначала выполните import")
	}
	selector, socksPort, httpPort, err := parseUpArgs(args)
	if err != nil {
		return err
	}
	profile := state.Subscription.ActiveProfile()
	if selector != "" {
		profile, err = resolveProfile(state.Subscription, selector)
		if err != nil {
			return err
		}
	}
	if profile == nil {
		return errors.New("профиль не выбран")
	}

	assetDir, err := storage.DefaultAssetDir()
	if err != nil {
		return err
	}
	if xrayconf.UsesGeoAssets(profile.JSON) {
		if err := assets.Ensure(assetDir); err != nil {
			return err
		}
	}
	core := engine.New(assetDir)
	core.SocksPort = socksPort
	if httpPort != 0 {
		core.HTTPPort = httpPort
		core.ForceHTTPPort = true
	}
	ports, err := core.Start(ctx, profile.JSON)
	if err != nil {
		return err
	}
	defer core.Stop()

	fmt.Printf("Профиль: %s\n", profile.Remarks)
	fmt.Printf("SOCKS: 127.0.0.1:%d\n", ports.Socks)
	fmt.Printf("HTTP:  127.0.0.1:%d\n", ports.HTTP)
	fmt.Println("Ctrl-C — остановить")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("Остановка")
			return nil
		case <-ticker.C:
			if !core.Healthy(ctx) {
				return errors.New("SOCKS-инбаунд перестал отвечать")
			}
		}
	}
}

func status(store *storage.Store) error {
	state, err := store.Load()
	if err != nil {
		return err
	}
	fmt.Printf("Файл состояния: %s\n", store.Path())
	fmt.Printf("Подписка:       %s\n", orDash(state.Subscription.URL))
	fmt.Printf("Название:       %s\n", orDash(state.Subscription.Title))
	fmt.Printf("Профилей:       %d\n", len(state.Subscription.Profiles))
	if active := state.Subscription.ActiveProfile(); active != nil {
		fmt.Printf("Активный:       %s\n", active.Remarks)
	}
	if state.Subscription.UpdatedAt > 0 {
		updated := time.UnixMilli(state.Subscription.UpdatedAt)
		fmt.Printf("Обновлена:      %s\n", updated.Format(time.RFC3339))
	}
	fmt.Printf("Авторизован:    %v\n", state.Subscription.Authorized())
	fmt.Printf("Telegram:       %s\n", tgws.ParseTransport(state.Telegram.Transport).Title())
	return nil
}

func check(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return errors.New("нужен порт SOCKS")
	}
	target := "https://www.gstatic.com/generate_204"
	if len(args) > 1 {
		target = args[1]
	}
	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", args[0]), nil, proxy.Direct)
	if err != nil {
		return fmt.Errorf("SOCKS-клиент не создан: %w", err)
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return errors.New("SOCKS-клиент не поддерживает контекст")
	}
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{DialContext: contextDialer.DialContext},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("запрос не прошёл: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	fmt.Printf("HTTP %d, %d байт, %d мс\n",
		response.StatusCode, len(body), time.Since(started).Milliseconds())
	return nil
}

func telegramLink(store *storage.Store) error {
	state, err := store.Load()
	if err != nil {
		return err
	}
	secret, err := tgws.EnsureSecret(state.Telegram.Secret)
	if err != nil {
		return err
	}
	if secret != state.Telegram.Secret {
		if _, err := store.Update(func(target *storage.State) {
			target.Telegram.Secret = secret
		}); err != nil {
			return err
		}
	}
	link, err := tgws.ProxyURI(secret, false)
	if err != nil {
		return err
	}
	fmt.Println(link)
	return nil
}

// parseUpArgs reads "up [профиль] [--socks порт] [--http порт]".
func parseUpArgs(args []string) (selector string, socksPort, httpPort int, err error) {
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "--socks", "--http":
			if index+1 >= len(args) {
				return "", 0, 0, fmt.Errorf("после %s нужен номер порта", argument)
			}
			index++
			port, convertErr := strconv.Atoi(args[index])
			if convertErr != nil || port < 1 || port > 65535 {
				return "", 0, 0, fmt.Errorf("некорректный порт %q", args[index])
			}
			if argument == "--socks" {
				socksPort = port
			} else {
				httpPort = port
			}
		default:
			if strings.HasPrefix(argument, "-") {
				return "", 0, 0, fmt.Errorf("неизвестный флаг %q", argument)
			}
			if selector != "" {
				return "", 0, 0, errors.New("указано больше одного профиля")
			}
			selector = argument
		}
	}
	return selector, socksPort, httpPort, nil
}

// pingProfiles measures every profile the way the servers screen does.
func pingProfiles(ctx context.Context, store *storage.Store) error {
	state, err := store.Load()
	if err != nil {
		return err
	}
	if len(state.Subscription.Profiles) == 0 {
		return errors.New("подписка не импортирована")
	}
	assetDir, err := storage.DefaultAssetDir()
	if err != nil {
		return err
	}
	measurer := &ping.Measurer{AssetDir: assetDir}
	results := measurer.MeasureAll(ctx, state.Subscription.Profiles)
	for index, result := range results {
		profile := state.Subscription.Profiles[index]
		name := subscription.ParseServerRemark(profile.Remarks).Name
		if result.OK() {
			fmt.Printf("%-28s %5d мс\n", name, result.LatencyMS)
			continue
		}
		fmt.Printf("%-28s %s\n", name, result.Error)
	}
	return nil
}

// telegramRun brings up the MTProto proxy in the foreground, for verifying a transport
// against a real Telegram client.
func telegramRun(ctx context.Context, store *storage.Store, args []string) error {
	state, err := store.Load()
	if err != nil {
		return err
	}
	secret, err := tgws.EnsureSecret(state.Telegram.Secret)
	if err != nil {
		return err
	}
	if secret != state.Telegram.Secret {
		if _, err := store.Update(func(target *storage.State) {
			target.Telegram.Secret = secret
		}); err != nil {
			return err
		}
	}
	transport := tgws.ParseTransport(state.Telegram.Transport)
	if len(args) == 1 {
		transport = tgws.ParseTransport(args[0])
	}
	cacheDir, err := storage.DefaultCacheDir()
	if err != nil {
		return err
	}

	proxy := &tgws.Proxy{}
	if err := proxy.Start(ctx, tgws.Settings{
		Secret:           secret,
		Transport:        transport,
		CloudflareDomain: state.Telegram.CloudflareDomain,
		CacheDir:         cacheDir,
		Dial:             netbypass.Dialer(),
	}); err != nil {
		return err
	}
	defer proxy.Stop()

	link, err := tgws.ProxyURI(secret, false)
	if err != nil {
		return err
	}
	fmt.Printf("Транспорт: %s\n", transport.Title())
	fmt.Printf("Порт:      127.0.0.1:%d\n", proxy.Port())
	fmt.Printf("Ссылка:    %s\n", link)
	fmt.Println("Ctrl-C — остановить")
	<-ctx.Done()
	fmt.Println("\n" + tgws.Summary())
	return nil
}

// tunCommand talks to the privileged helper, so the tunnel can be verified without the GUI.
func tunCommand(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return errors.New("нужна команда: status, up или down")
	}
	client := ipc.NewClient("")
	defer client.Close()

	switch args[0] {
	case "status":
		status, err := client.Status(ctx)
		if err != nil {
			return err
		}
		printTunStatus(status)
		return nil
	case "down":
		status, err := client.TunDown(ctx)
		if err != nil {
			return err
		}
		printTunStatus(status)
		return nil
	case "up":
		if len(args) != 2 {
			return errors.New("нужен порт SOCKS: sa05ctl tun up <порт>")
		}
		port, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("некорректный порт %q", args[1])
		}
		status, err := client.TunUp(ctx, ipc.TunUp{
			SocksPort:  port,
			DNS:        "1.1.1.1",
			KillSwitch: true,
			BypassMark: netbypass.Mark,
		})
		if err != nil {
			return err
		}
		printTunStatus(status)
		return nil
	default:
		return fmt.Errorf("неизвестная команда %q", args[0])
	}
}

func printTunStatus(status ipc.Status) {
	fmt.Printf("Протокол:  %d\n", status.Version)
	fmt.Printf("Туннель:   %v\n", status.TunUp)
	if status.Interface != "" {
		fmt.Printf("Интерфейс: %s -> 127.0.0.1:%d\n", status.Interface, status.SocksPort)
	}
	if status.Message != "" {
		fmt.Printf("Сообщение: %s\n", status.Message)
	}
}

func resolveProfile(state subscription.State, selector string) (*subscription.Profile, error) {
	selector = strings.TrimSpace(selector)
	for index := range state.Profiles {
		if state.Profiles[index].ID == selector {
			return &state.Profiles[index], nil
		}
	}
	var number int
	if _, err := fmt.Sscanf(selector, "%d", &number); err == nil {
		if number >= 1 && number <= len(state.Profiles) {
			return &state.Profiles[number-1], nil
		}
	}
	for index := range state.Profiles {
		if strings.HasPrefix(state.Profiles[index].ID, selector) {
			return &state.Profiles[index], nil
		}
	}
	return nil, fmt.Errorf("профиль %q не найден", selector)
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}
