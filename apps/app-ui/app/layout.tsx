import type { Metadata } from "next";
import "./globals.css";
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@kdelta/ui/components/sidebar";
import { Separator } from "@kdelta/ui/components/separator";
import { Providers } from "./providers";
import { AppSidebar } from "./sidebar";

export const metadata: Metadata = {
  title: "kdelta",
  description:
    "Detect deployed Kubernetes resources and analyze their version deltas",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body className="min-h-screen bg-background text-foreground antialiased">
        <Providers>
          <SidebarProvider>
            <AppSidebar />
            <SidebarInset>
              <header className="flex h-14 shrink-0 items-center gap-2 border-b border-border px-4">
                <SidebarTrigger />
                <Separator orientation="vertical" className="mr-1 h-4" />
                <span className="text-sm text-muted-foreground">
                  Kubernetes version deltas
                </span>
              </header>
              <main className="min-w-0 flex-1 px-8 py-8">{children}</main>
            </SidebarInset>
          </SidebarProvider>
        </Providers>
      </body>
    </html>
  );
}
