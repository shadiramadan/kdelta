"use client";

import { TransportProvider } from "@connectrpc/connect-query";
import { createConnectTransport } from "@connectrpc/connect-web";
import { Code, ConnectError } from "@connectrpc/connect";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// Relative base URL: `next dev` proxies /rpc/* to the Go server; the static
// export is served by the Go server itself, so the same origin answers both.
const transport = createConnectTransport({ baseUrl: "/rpc" });

// Terminal Connect codes describe a state the caller must change (no scan
// cached, bad argument, missing resource) — retrying only delays the message.
const terminalCodes = new Set([
  Code.FailedPrecondition,
  Code.InvalidArgument,
  Code.NotFound,
  Code.Unimplemented,
  Code.PermissionDenied,
  Code.Unauthenticated,
]);

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: (failureCount, error) => {
        if (error instanceof ConnectError && terminalCodes.has(error.code)) {
          return false;
        }
        return failureCount < 1;
      },
    },
  },
});

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </TransportProvider>
  );
}
