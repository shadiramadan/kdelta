import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "kdelta — Kubernetes upgrade impact analysis",
  description:
    "What's deployed, what version is it, what changed upstream, and what breaks if you upgrade it. One binary: CLI, API, and web UI.",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body className="min-h-screen bg-background text-foreground antialiased">
        {children}
      </body>
    </html>
  );
}
