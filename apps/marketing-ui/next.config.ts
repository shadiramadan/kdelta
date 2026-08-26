import type { NextConfig } from "next";

// Always a pure static export: this site deploys to GitHub Pages behind the
// kdelta.dev custom domain (so no basePath) and needs no API proxying.
const config: NextConfig = {
  output: "export",
  images: { unoptimized: true },
  transpilePackages: ["@kdelta/ui"],
};

export default config;
