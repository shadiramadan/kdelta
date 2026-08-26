"use client";

import { useState } from "react";
import { useMutation } from "@connectrpc/connect-query";
import { KdeltaService } from "@kdelta/api";
import { Button } from "@kdelta/ui/components/button";

export default function EchoPage() {
  const [message, setMessage] = useState("hello, cluster");
  const echo = useMutation(KdeltaService.method.echo);

  return (
    <div className="mx-auto max-w-xl space-y-6">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">Echo</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Round-trips a message through the ConnectRPC placeholder service —
          the API pipeline liveness check.
        </p>
      </header>

      <section className="rounded-xl border border-border bg-card/60 p-6">
        <form
          onSubmit={(event) => {
            event.preventDefault();
            echo.mutate({ message });
          }}
          className="flex gap-2"
        >
          <input
            value={message}
            onChange={(event) => setMessage(event.target.value)}
            placeholder="say something"
            className="min-w-0 flex-1 rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none focus:border-ring"
          />
          <Button type="submit" disabled={echo.isPending || message.length === 0}>
            {echo.isPending ? "Sending…" : "Echo"}
          </Button>
        </form>

        {echo.data !== undefined && (
          <p className="mt-4 rounded-lg border border-primary/30 bg-primary/10 px-3 py-2 font-mono text-sm text-primary">
            {echo.data.message}
          </p>
        )}
        {echo.error !== null && (
          <p className="mt-4 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 font-mono text-sm text-destructive">
            {String(echo.error)}
          </p>
        )}
      </section>
    </div>
  );
}
