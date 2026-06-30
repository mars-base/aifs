// Wails runtime bridge — production build uses the injected runtime.
// In dev mode (vite serve) we fall back to a mock so the app loads without crashing.

// @ts-ignore
const wailsRuntime = (window as any).runtime

export const EventsOn = (eventName: string, callback: (...args: unknown[]) => void) =>
  wailsRuntime?.EventsOn(eventName, callback)

export const EventsEmit = (eventName: string, ...args: unknown[]) =>
  wailsRuntime?.EventsEmit(eventName, ...args)

export const BrowserOpenURL = (url: string) =>
  wailsRuntime?.BrowserOpenURL(url)
