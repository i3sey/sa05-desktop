// Package app is the controller the UI talks to.
//
// It owns the runtime: the subscription cache, the Xray core, the state store and the
// latency probes. It deliberately knows nothing about Wails or the terminal, so the GUI
// and sa05ctl drive the exact same transitions.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/fife/sa05-desktop/internal/assets"
	"github.com/fife/sa05-desktop/internal/core/diag"
	"github.com/fife/sa05-desktop/internal/core/engine"
	"github.com/fife/sa05-desktop/internal/core/ping"
	"github.com/fife/sa05-desktop/internal/core/recovery"
	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/core/subscription"
	"github.com/fife/sa05-desktop/internal/core/xrayconf"
	"github.com/fife/sa05-desktop/internal/desktop"
	"github.com/fife/sa05-desktop/internal/ipc"
	"github.com/fife/sa05-desktop/internal/netbypass"
	"github.com/fife/sa05-desktop/internal/notify"
	"github.com/fife/sa05-desktop/internal/storage"
	"github.com/fife/sa05-desktop/internal/sysproxy"
	"github.com/fife/sa05-desktop/internal/tgws"
)

const healthInterval = 5 * time.Second

// ProfileView is one row of the servers screen.
type ProfileView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Flag      string `json:"flag"`
	Remarks   string `json:"remarks"`
	Active    bool   `json:"active"`
	LatencyMS int    `json:"latencyMs"`
	Error     string `json:"error"`
}

// SubscriptionView is what the UI shows about the subscription itself.
type SubscriptionView struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	UserInfo   string `json:"userInfo"`
	UpdatedAt  int64  `json:"updatedAt"`
	Authorized bool   `json:"authorized"`
	Count      int    `json:"count"`
}

// View is the whole UI model: state, presentation, toggles and profiles in one payload,
// so the frontend never has to stitch several calls together.
type View struct {
	Snapshot     state.Snapshot     `json:"snapshot"`
	Presentation state.Presentation `json:"presentation"`
	Subscription SubscriptionView   `json:"subscription"`
	Profiles     []ProfileView      `json:"profiles"`
	Toggles      storage.Toggles    `json:"toggles"`
	Telegram     TelegramView       `json:"telegram"`
	// HelperAvailable tells the UI whether the privileged component is installed, so a
	// disabled TUN toggle can explain itself instead of failing on click.
	HelperAvailable bool `json:"helperAvailable"`
}

// TelegramView describes the built-in MTProto proxy for the UI.
type TelegramView struct {
	Transport string `json:"transport"`
	Port      int    `json:"port"`
	Link      string `json:"link"`
	Applied   bool   `json:"applied"`
}

// App is the controller.
type App struct {
	assetDir string
	store    *storage.Store
	states   *state.Store
	core     *engine.Engine
	measurer *ping.Measurer
	session  desktop.Integration
	proxy    sysproxy.Controller
	telegram *tgws.Proxy
	helper   *ipc.Client
	notifier *notify.Notifier
	// telegramPort overrides the fixed MTProto port; tests set it so they never bind the
	// real 1443 on a developer's machine.
	telegramPort int

	mutex       sync.Mutex
	latency     map[string]ping.Result
	diagTargets []diag.Target
	trafficStop context.CancelFunc
	monitor     context.CancelFunc
	rootCtx     context.Context
	rootStop    context.CancelFunc
	connectMu   sync.Mutex
}

// New wires the controller. assetDir holds geoip.dat / geosite.dat.
func New(store *storage.Store, assetDir string) *App {
	core := engine.New(assetDir)
	// Another proxy client may already hold the provider's port; rebinding beats failing.
	core.AutoPort = true
	// Mark the core's own sockets so they keep using the real interface once the TUN
	// device holds the default route.
	core.OutboundMark = netbypass.Mark
	rootCtx, rootStop := context.WithCancel(context.Background())
	// Session integration is optional: a headless run still controls the tunnel, it just
	// cannot register autostart or the sa05:// handler.
	session, err := desktop.New()
	if err != nil {
		session = nil
	}
	application := &App{
		assetDir: assetDir,
		store:    store,
		states:   state.NewStore(),
		core:     core,
		measurer: &ping.Measurer{AssetDir: assetDir},
		session:  session,
		telegram: &tgws.Proxy{},
		helper:   ipc.NewClient(""),
		notifier: notify.New(),
		latency:  map[string]ping.Result{},
		rootCtx:  rootCtx,
		rootStop: rootStop,
	}
	// A previous run may have died with the desktop still pointed at SA05's ports.
	if err := application.restoreSystemProxy(); err != nil {
		log.Printf("прежние настройки прокси не восстановлены: %v", err)
	}
	return application
}

// Snapshot is the current runtime state.
func (a *App) Snapshot() state.Snapshot { return a.states.Snapshot() }

// RegisterURLScheme makes sa05:// links open this client. Best effort: a headless or
// unusual session simply keeps the links unregistered.
func (a *App) RegisterURLScheme() error {
	if a.session == nil {
		return errors.New("Интеграция с сеансом недоступна")
	}
	return a.session.RegisterURLScheme()
}

// Subscribe registers a state listener; the returned function cancels it.
func (a *App) Subscribe(listener func(state.Snapshot)) func() {
	return a.states.Subscribe(listener)
}

// Shutdown stops the core and every background job. Safe to call twice.
func (a *App) Shutdown() {
	if err := a.disableTun(context.Background()); err != nil {
		log.Printf("туннель не снят: %v", err)
	}
	if err := a.disableSystemProxy(); err != nil {
		log.Printf("системный прокси не снят: %v", err)
	}
	a.telegram.Stop()
	a.helper.Close()
	a.stopTrafficMeter()
	a.stopMonitor()
	a.core.Stop()
	a.rootStop()
}

// View builds the full UI model.
func (a *App) View() (View, error) {
	stored, err := a.store.Load()
	if err != nil {
		return View{}, err
	}
	snapshot := a.states.Snapshot()
	telegram, err := a.telegramView(stored)
	if err != nil {
		return View{}, err
	}
	return View{
		Snapshot:        snapshot,
		Presentation:    state.Present(snapshot),
		Subscription:    subscriptionView(stored.Subscription),
		Profiles:        a.profileViews(stored.Subscription),
		Toggles:         stored.Toggles,
		Telegram:        telegram,
		HelperAvailable: a.HelperAvailable(context.Background()),
	}, nil
}

// ImportSubscription fetches and caches a subscription. A failure leaves the previous
// cache untouched, which is why the store is only written on success.
func (a *App) ImportSubscription(ctx context.Context, rawURL string) error {
	stored, err := a.store.Load()
	if err != nil {
		return err
	}
	target := rawURL
	if deepLink := subscription.ParseDeepLink(rawURL); deepLink != "" {
		target = deepLink
	}
	result, err := subscription.NewClient().Update(ctx, target, stored.Subscription)
	if err != nil {
		return err
	}
	_, err = a.store.Update(func(next *storage.State) {
		next.Subscription = result.State
	})
	return err
}

// SelectProfile changes the active server, reconnecting when the tunnel is up.
func (a *App) SelectProfile(ctx context.Context, id string) error {
	stored, err := a.store.Load()
	if err != nil {
		return err
	}
	found := false
	for _, profile := range stored.Subscription.Profiles {
		if profile.ID == id {
			found = true
			break
		}
	}
	if !found {
		return errors.New("Профиль не найден")
	}
	if stored.Subscription.ActiveProfileID == id {
		return nil
	}
	if _, err := a.store.Update(func(next *storage.State) {
		next.Subscription.ActiveProfileID = id
	}); err != nil {
		return err
	}
	if a.states.Snapshot().Requested() {
		return a.Connect(ctx)
	}
	a.states.Update(func(snapshot *state.Snapshot) {
		snapshot.ProfileID = id
		snapshot.ProfileName = profileName(stored.Subscription, id)
	})
	return nil
}

// Connect starts (or restarts) the core on the active profile.
func (a *App) Connect(ctx context.Context) error {
	a.connectMu.Lock()
	defer a.connectMu.Unlock()

	stored, err := a.store.Load()
	if err != nil {
		return err
	}
	if !stored.Subscription.Authorized() {
		a.fail(state.FailureAuthorization, "Нужна действующая подписка")
		return errors.New("Нужна действующая подписка")
	}
	profile := stored.Subscription.ActiveProfile()
	if profile == nil {
		a.fail(state.FailureAuthorization, "Профиль не выбран")
		return errors.New("Профиль не выбран")
	}

	name := subscription.ParseServerRemark(profile.Remarks).Name
	a.states.Update(func(snapshot *state.Snapshot) {
		snapshot.Status = state.StatusConnecting
		snapshot.ProfileID = profile.ID
		snapshot.ProfileName = name
		snapshot.Message = ""
		snapshot.FailureKind = state.FailureNone
		snapshot.Components = []state.ComponentSnapshot{
			{Component: state.ComponentXray, Status: state.ComponentStarting},
		}
	})

	// Only profiles that reference geoip:/geosite: need the databases; requiring them for
	// every profile would block a working config over an unused feature.
	if xrayconf.UsesGeoAssets(profile.JSON) {
		if err := assets.Ensure(a.assetDir); err != nil {
			a.fail(state.FailureService, err.Error())
			return err
		}
	}

	a.stopMonitor()
	ports, err := a.core.Start(ctx, profile.JSON)
	if err != nil {
		a.fail(state.FailureBackend, err.Error())
		a.notifier.Send(notify.KindError, "SA05", "Не удалось подключиться: "+err.Error())
		return err
	}
	a.states.Update(func(snapshot *state.Snapshot) {
		snapshot.Status = state.StatusConnected
		snapshot.SocksPort = ports.Socks
		snapshot.HTTPPort = ports.HTTP
		snapshot.ConnectedAt = time.Now().UnixMilli()
		snapshot.RecoveryAttempt = 0
		snapshot.Message = ""
		snapshot.Components = []state.ComponentSnapshot{
			{Component: state.ComponentXray, Status: state.ComponentRunning},
		}
	})
	a.startMonitor()
	a.startTrafficMeter(a.rootCtx)
	a.notifier.Send(notify.KindInfo, "SA05", "Подключено: "+name)
	return nil
}

// Disconnect stops the core and clears the state. The system proxy and the tunnel go with
// it: routing that outlives the core would send traffic into a dead port.
func (a *App) Disconnect() {
	if err := a.disableTun(context.Background()); err != nil {
		log.Printf("туннель не снят: %v", err)
	}
	if err := a.disableSystemProxy(); err != nil {
		log.Printf("системный прокси не снят: %v", err)
	}
	a.connectMu.Lock()
	defer a.connectMu.Unlock()
	a.stopMonitor()
	a.stopTrafficMeter()
	a.core.Stop()
	// Telegram is independent of the tunnel, so its state survives a disconnect.
	a.states.Update(func(next *state.Snapshot) {
		telegramOn := next.TelegramOn
		components := []state.ComponentSnapshot{}
		if telegramOn {
			components = append(components, state.ComponentSnapshot{
				Component: state.ComponentTelegram,
				Status:    state.ComponentRunning,
			})
		}
		*next = state.Snapshot{
			Status:     state.StatusDisconnected,
			TelegramOn: telegramOn,
			Components: components,
		}
	})
}

// Toggle switches one of the main-screen switches. Unimplemented toggles report a clear
// error instead of silently pretending to be on.
func (a *App) Toggle(ctx context.Context, name string, enabled bool) error {
	switch name {
	case "systemProxy":
		if enabled {
			return a.enableSystemProxy()
		}
		return a.disableSystemProxy()
	case "tun":
		if enabled {
			return a.enableTun(ctx)
		}
		return a.disableTun(ctx)
	case "telegram":
		if enabled {
			return a.enableTelegram(ctx)
		}
		return a.disableTelegram()
	case "autostart":
		if a.session == nil {
			return errors.New("Интеграция с сеансом недоступна")
		}
		if err := a.session.SetAutostart(enabled); err != nil {
			return err
		}
		_, err := a.store.Update(func(next *storage.State) {
			next.Toggles.Autostart = enabled
		})
		return err
	case "autoConnect", "autoUpdate", "killSwitch", "allowIpv6Bypass":
		_, err := a.store.Update(func(next *storage.State) {
			switch name {
			case "autoConnect":
				next.Toggles.AutoConnect = enabled
			case "autoUpdate":
				next.Toggles.AutoUpdate = enabled
			case "killSwitch":
				next.Toggles.KillSwitch = enabled
			case "allowIpv6Bypass":
				next.Toggles.AllowIPv6Bypass = enabled
			}
		})
		return err
	default:
		return fmt.Errorf("неизвестный переключатель %q", name)
	}
}

// PingProfiles measures every profile and caches the results for the servers screen.
func (a *App) PingProfiles(ctx context.Context) ([]ProfileView, error) {
	stored, err := a.store.Load()
	if err != nil {
		return nil, err
	}
	if len(stored.Subscription.Profiles) == 0 {
		return nil, errors.New("Подписка не импортирована")
	}
	results := a.measurer.MeasureAll(ctx, stored.Subscription.Profiles)

	a.mutex.Lock()
	for _, result := range results {
		a.latency[result.ProfileID] = result
	}
	a.mutex.Unlock()

	if active := stored.Subscription.ActiveProfile(); active != nil {
		for _, result := range results {
			if result.ProfileID == active.ID && result.OK() {
				a.states.Update(func(snapshot *state.Snapshot) {
					snapshot.LatencyMS = result.LatencyMS
				})
			}
		}
	}
	return a.profileViews(stored.Subscription), nil
}

// SelectFastest measures every profile and switches to the quickest one that answered.
func (a *App) SelectFastest(ctx context.Context) (string, error) {
	profiles, err := a.PingProfiles(ctx)
	if err != nil {
		return "", err
	}
	best := ProfileView{}
	for _, profile := range profiles {
		if profile.Error != "" || profile.LatencyMS <= 0 {
			continue
		}
		if best.ID == "" || profile.LatencyMS < best.LatencyMS {
			best = profile
		}
	}
	if best.ID == "" {
		return "", errors.New("Ни один профиль не ответил")
	}
	if err := a.SelectProfile(ctx, best.ID); err != nil {
		return "", err
	}
	return best.ID, nil
}

// TelegramLink returns the tg:// link, generating and persisting the secret on first use.
func (a *App) TelegramLink() (string, error) {
	stored, err := a.store.Load()
	if err != nil {
		return "", err
	}
	secret, err := tgws.EnsureSecret(stored.Telegram.Secret)
	if err != nil {
		return "", err
	}
	if secret != stored.Telegram.Secret {
		if _, err := a.store.Update(func(next *storage.State) {
			next.Telegram.Secret = secret
		}); err != nil {
			return "", err
		}
	}
	return tgws.ProxyURI(secret, false)
}

// SetTelegramTransport records which upstream the MTProto proxy should use and re-applies
// it immediately when the proxy is running.
func (a *App) SetTelegramTransport(ctx context.Context, value string) error {
	transport := tgws.ParseTransport(value)
	if _, err := a.store.Update(func(next *storage.State) {
		next.Telegram.Transport = string(transport)
	}); err != nil {
		return err
	}
	return a.restartTelegramIfRunning(ctx)
}

// startMonitor watches the SOCKS inbound and reconnects a dead core, bounded by the
// recovery policy so a permanently broken profile surfaces as an error.
func (a *App) startMonitor() {
	ctx, cancel := context.WithCancel(a.rootCtx)
	a.mutex.Lock()
	a.monitor = cancel
	a.mutex.Unlock()

	go func() {
		ticker := time.NewTicker(healthInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if a.core.Healthy(ctx) {
					continue
				}
				snapshot := a.states.Snapshot()
				if snapshot.Status != state.StatusConnected {
					continue
				}
				decision := recovery.RouteChecked(false, snapshot.RecoveryAttempt)
				if decision != recovery.DecisionReconnect {
					a.fail(state.FailureHealthCheck, "Соединение с сервером не восстановилось")
					a.notifier.Send(notify.KindError, "SA05",
						"Соединение потеряно и не восстановилось")
					return
				}
				a.notifier.Send(notify.KindWarning, "SA05", "Соединение потеряно, переподключаемся")
				a.states.Update(func(next *state.Snapshot) {
					next.Status = state.StatusRecovering
					next.RecoveryAttempt = snapshot.RecoveryAttempt + 1
					next.Message = "Переподключаемся"
					next.Components = []state.ComponentSnapshot{
						{Component: state.ComponentXray, Status: state.ComponentFailed},
					}
				})
				// Not ctx: Connect cancels this monitor (and with it ctx) before it
				// restarts the core, so the restart must not depend on it.
				if err := a.reconnect(snapshot.RecoveryAttempt + 1); err != nil {
					return
				}
				return
			}
		}
	}()
}

// reconnect restarts the core, preserving the attempt counter so repeated failures hit
// the policy bound instead of looping forever.
func (a *App) reconnect(attempt int) error {
	if err := a.Connect(a.rootCtx); err != nil {
		return err
	}
	a.states.Update(func(snapshot *state.Snapshot) {
		snapshot.RecoveryAttempt = attempt
	})
	return nil
}

func (a *App) stopMonitor() {
	a.mutex.Lock()
	cancel := a.monitor
	a.monitor = nil
	a.mutex.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) fail(kind state.FailureKind, message string) {
	a.states.Update(func(snapshot *state.Snapshot) {
		snapshot.Status = state.StatusError
		snapshot.FailureKind = kind
		snapshot.Message = message
		snapshot.SocksPort = 0
		snapshot.HTTPPort = 0
		snapshot.Components = []state.ComponentSnapshot{
			{Component: state.ComponentXray, Status: state.ComponentFailed},
		}
	})
}

// probeThroughTunnel checks that the tunnel carries traffic, not merely that its port is
// open. A core whose server became unreachable keeps accepting connections locally.
func (a *App) probeThroughTunnel(ctx context.Context) bool {
	snapshot := a.states.Snapshot()
	if snapshot.SocksPort == 0 {
		return false
	}
	_, err := ping.Probe(ctx, snapshot.SocksPort, xrayconf.DefaultProbeURL)
	return err == nil
}

func (a *App) profileViews(cached subscription.State) []ProfileView {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	active := cached.ActiveProfile()
	views := make([]ProfileView, 0, len(cached.Profiles))
	for _, profile := range cached.Profiles {
		remark := subscription.ParseServerRemark(profile.Remarks)
		view := ProfileView{
			ID:      profile.ID,
			Name:    remark.Name,
			Flag:    remark.Flag,
			Remarks: profile.Remarks,
			Active:  active != nil && active.ID == profile.ID,
		}
		if result, ok := a.latency[profile.ID]; ok {
			view.LatencyMS = result.LatencyMS
			view.Error = result.Error
		}
		views = append(views, view)
	}
	return views
}

func (a *App) telegramView(stored storage.State) (TelegramView, error) {
	view := TelegramView{
		Transport: string(tgws.ParseTransport(stored.Telegram.Transport)),
		Port:      tgws.Port,
		Applied:   stored.Telegram.Applied,
	}
	if tgws.ValidSecret(stored.Telegram.Secret) {
		link, err := tgws.ProxyURI(stored.Telegram.Secret, false)
		if err != nil {
			return TelegramView{}, err
		}
		view.Link = link
	}
	return view, nil
}

func subscriptionView(cached subscription.State) SubscriptionView {
	return SubscriptionView{
		URL:        cached.URL,
		Title:      cached.Title,
		UserInfo:   cached.UserInfo,
		UpdatedAt:  cached.UpdatedAt,
		Authorized: cached.Authorized(),
		Count:      len(cached.Profiles),
	}
}

func profileName(cached subscription.State, id string) string {
	for _, profile := range cached.Profiles {
		if profile.ID == id {
			return subscription.ParseServerRemark(profile.Remarks).Name
		}
	}
	return ""
}
