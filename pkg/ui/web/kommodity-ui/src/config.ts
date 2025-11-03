const RUNTIME = (typeof window !== "undefined" && window.__ENV__) || {}

export const KOMMODITY_BASE_URL =
    RUNTIME["KOMMODITY_BASE_URL"] ??
    import.meta.env.VITE_KOMMODITY_BASE_URL ?? 
    "http://localhost:5000"
