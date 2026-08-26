import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "kdelta",
  description:
    "Detect deployed Kubernetes resources, resolve their versions, and assess the impact of upgrading them.",
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
