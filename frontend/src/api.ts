// Bridge to the Go controller.
//
// Wails injects window.go.main.App with one function per exported method. When the page
// is opened in a plain browser (npm run dev) those are missing, so a small mock keeps the
// UI developable without building the Go binary.

export type RunStatus =
  | 'DISCONNECTED'
  | 'CONNECTING'
  | 'CONNECTED'
  | 'RECOVERING'
  | 'WAITING_FOR_NETWORK'
  | 'ERROR'

export interface ComponentSnapshot {
  component: string
  status: string
}

export interface Snapshot {
  status: RunStatus
  profileId: string
  profileName: string
  message: string
  failureKind: string
  socksPort: number
  httpPort: number
  connectedAt: number
  recoveryAttempt: number
  systemProxyOn: boolean
  tunOn: boolean
  telegramOn: boolean
  latencyMs: number
  components: ComponentSnapshot[] | null
}

export interface Presentation {
  title: string
  description: string
  primaryAction: 'CONNECT' | 'STOP' | 'RETRY' | 'OPEN_SUBSCRIPTION'
  secondaryActions: string[] | null
}

export interface ProfileView {
  id: string
  name: string
  flag: string
  remarks: string
  active: boolean
  latencyMs: number
  error: string
}

export interface SubscriptionView {
  url: string
  title: string
  userInfo: string
  updatedAt: number
  authorized: boolean
  count: number
}

export interface Toggles {
  systemProxy: boolean
  tun: boolean
  telegram: boolean
  allowIpv6Bypass: boolean
  killSwitch: boolean
  autoConnect: boolean
  autostart: boolean
  autoUpdate: boolean
}

export interface TelegramView {
  transport: string
  port: number
  link: string
  applied: boolean
}

export interface View {
  snapshot: Snapshot
  presentation: Presentation
  subscription: SubscriptionView
  profiles: ProfileView[] | null
  toggles: Toggles
  telegram: TelegramView
  helperAvailable: boolean
}

interface Backend {
  View(): Promise<View>
  Import(url: string): Promise<void>
  Connect(): Promise<void>
  Disconnect(): Promise<void>
  SelectProfile(id: string): Promise<void>
  SelectFastest(): Promise<string>
  PingProfiles(): Promise<ProfileView[]>
  Toggle(name: string, enabled: boolean): Promise<void>
  TelegramLink(): Promise<string>
  SetTelegramTransport(value: string): Promise<void>
  OpenURL(url: string): Promise<void>
  Quit(): Promise<void>
}

declare global {
  interface Window {
    go?: { main?: { App?: Backend } }
    runtime?: {
      EventsOn(event: string, callback: (...data: unknown[]) => void): () => void
      ClipboardSetText(text: string): Promise<boolean>
    }
  }
}

const mockView: View = {
  snapshot: {
    status: 'DISCONNECTED',
    profileId: '',
    profileName: '',
    message: '',
    failureKind: 'NONE',
    socksPort: 0,
    httpPort: 0,
    connectedAt: 0,
    recoveryAttempt: 0,
    systemProxyOn: false,
    tunOn: false,
    telegramOn: false,
    latencyMs: 0,
    components: [],
  },
  presentation: {
    title: 'Отключено',
    description: 'Выберите сервер и подключитесь',
    primaryAction: 'CONNECT',
    secondaryActions: [],
  },
  subscription: { url: '', title: '', userInfo: '', updatedAt: 0, authorized: false, count: 0 },
  profiles: [],
  toggles: {
    systemProxy: false,
    tun: false,
    telegram: false,
    allowIpv6Bypass: false,
    killSwitch: true,
    autoConnect: false,
    autostart: false,
    autoUpdate: true,
  },
  telegram: { transport: 'auto', port: 1443, link: '', applied: false },
  helperAvailable: false,
}

const mock: Backend = {
  View: async () => structuredClone(mockView),
  Import: async () => {
    throw new Error('Браузерный режим: бэкенд недоступен')
  },
  Connect: async () => {},
  Disconnect: async () => {},
  SelectProfile: async () => {},
  SelectFastest: async () => '',
  PingProfiles: async () => [],
  Toggle: async () => {},
  TelegramLink: async () => 'tg://proxy?server=127.0.0.1&port=1443&secret=dd…',
  SetTelegramTransport: async () => {},
  OpenURL: async () => {},
  Quit: async () => {},
}

export const backend: Backend = window.go?.main?.App ?? mock

export const isNative = Boolean(window.go?.main?.App)

/** onSnapshot subscribes to backend-pushed state; returns an unsubscribe function. */
export function onSnapshot(callback: (snapshot: Snapshot) => void): () => void {
  if (!window.runtime) return () => {}
  return window.runtime.EventsOn('state', (...data: unknown[]) => {
    callback(data[0] as Snapshot)
  })
}

export async function copyText(text: string): Promise<void> {
  if (window.runtime?.ClipboardSetText) {
    await window.runtime.ClipboardSetText(text)
    return
  }
  await navigator.clipboard.writeText(text)
}

/** errorText unwraps whatever Wails rejected a promise with into a readable string. */
export function errorText(error: unknown): string {
  if (typeof error === 'string') return error
  if (error instanceof Error) return error.message
  return String(error)
}
