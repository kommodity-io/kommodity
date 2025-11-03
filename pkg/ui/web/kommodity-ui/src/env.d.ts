export {}

declare global {
  interface Window {
    __ENV__?: Record<string, string>
  }
}
