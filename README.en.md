# TaskLoom

**An open-source, self-hosted R&D collaboration and project management tool for teams evaluating TAPD and Teambition alternatives.**

[中文文档](README.md) · [API documentation](docs/internal-api-reference.md) · [Contributing](CONTRIBUTING.md)

Connect requirements, agile iterations, bug tracking, test cases, discussions and delivery in one workspace.

## Highlights

- Requirement and sub-requirement management, personal templates and engineering role fields.
- Iterations, defects, test cases and execution records.
- AI-assisted requirement refinement, title generation, test generation and test review. Preview and human confirmation before adoption.
- Markdown/code editing, categorized attachments, JSON/Markdown requirement exports and API integrations for AI coding workflows.
- Personal work, search, notifications and optional external messaging services.
- TAPD PDF import with mapping review; compatibility depends on the input document.

## Get started

Use Node >=22.12, pnpm 11.19.0 and Go 1.27.1. Clone this repository, run `corepack pnpm install --frozen-lockfile`, `corepack pnpm build`, `go build -o server ./cmd/server`, `cp -R dist web`, then `node start.mjs`.

Open http://127.0.0.1:8080. Initial login: **Admin / 123456**. Password rotation is mandatory; do not expose the initial setup through a public reverse proxy. Demo members have independent random passwords. Data and session secrets are stored in the local data/ directory.

## Status

0.1.0-rc1 is a prerelease, not a production-readiness guarantee or a promise of feature parity with TAPD/Teambition. There is no affiliation with either product. Automatic code commits, AI subtask decomposition and change-impact analysis are roadmap items, not delivered features. External AI requires your own configuration and may incur provider fees.

Vue 3 / TypeScript + Go + SQLite. Web/H5 only for the initial community release. Licensed under Apache-2.0; third-party components retain their licenses.
