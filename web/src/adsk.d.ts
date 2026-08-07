// The Fusion palette JS bridge, injected by Fusion into pages hosted in a
// palette (absent everywhere else — always feature-detect). Declared once
// here so both the embed entry (src/embed/bridge.ts) and shared components
// (the composer's screenshot capture) see the same shape.
declare interface Window {
  adsk?: {
    fusionSendData: (action: string, data: string) => Promise<string> | string
  }
  fusionJavaScriptHandler?: {
    handle: (action: string, data: string) => string
  }
}
