import type { NextConfig } from "next";
import { PHASE_DEVELOPMENT_SERVER } from "next/constants";

const base: NextConfig = {
  transpilePackages: ["@kdelta/api", "@kdelta/ui"],
};

// Rewrites are unsupported (and error) alongside output:"export", so only
// production builds export statically; `next dev` instead proxies /rpc/* to
// the Go server — same relative URLs in both modes, no CORS.
const config = (phase: string): NextConfig => {
  if (phase === PHASE_DEVELOPMENT_SERVER) {
    return {
      ...base,
      // The rewrite proxy kills responses after 30s by default — far shorter
      // than a streamed changelog extraction or impact assessment. 10 minutes
      // covers the longest agentic flows.
      experimental: { proxyTimeout: 600_000 },
      // Dev-server gzip buffers proxied Connect streams until completion,
      // which would hide streamed progress from the browser (the production
      // Go server never transport-compresses streams).
      compress: false,
      async rewrites() {
        return [
          {
            source: "/rpc/:path*",
            destination: "http://localhost:8080/rpc/:path*",
          },
        ];
      },
    };
  }
  return {
    ...base,
    output: "export",
    images: { unoptimized: true },
  };
};

export default config;
