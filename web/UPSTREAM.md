# Vendored from Enterprise-Template

Source: https://github.com/Scicom-AI-Enterprise-Organization/Enterprise-Template
Commit: d6e424733cd626165ebe204c8c591573da5666df
Vendored: 2026-08-10

This directory is the Scicom Enterprise Template (Next.js 16 + Auth.js v5 + Prisma +
Tailwind v4 + Radix), carrying the shared design language and app shell. Keep changes
additive where possible so upstream fixes can be merged:

    git remote add template https://github.com/Scicom-AI-Enterprise-Organization/Enterprise-Template.git
    git fetch template && git diff d6e424733cd626165ebe204c8c591573da5666df..template/main -- .

bettersentryio-specific code lives in:
  src/app/(app)/monitors, src/app/(app)/incidents   pages
  src/lib/bsio.ts                                   Go API client
