# Changelog

## 0.2.0-rc1 — 2026-09-13

Community prerelease update:

- Vue desktop application plus an independent React mobile workbench: My work, Requirements, Iterations and Notifications, with scoped reading and notification actions.
- Full notification text and project-scoped personal API/MCP notification reads. Reads remain fixed to the credential owner and project, require their explicit scope, and do not mark messages read.
- AI PRD and defect refinement with outgoing-text review, selective append previews and manual saving; no automatic work-item mutations.
- Revocable seven-day requirement text snapshots that exclude internal people, comments and authenticated attachment links, and recheck the creator's access.
- Release-note review/export, administrator-controlled WeCom self-built-app configuration, and workload trends that must not be treated as individual performance evaluations.
- Community branding, clean desktop/mobile builds, synthetic demo identities and the retained Admin / 123456 mandatory first-password change protection.
- React license notices and community deployment/maintenance documentation. The test runner continues to discover all frontend suites automatically.

Test-boundary changes: removed `scripts/test-developer-bundle.mjs`, which verified a commercial Flutter/macOS source bundle absent from this Web-only release. Retained product assertions while replacing portal URLs, missing native-bridge source checks, the private local-service launcher and in-place production build assumptions with checks of the actual community entries and launcher. No product suites are excluded by the runner.

Acceptance limits: AI and WeCom require deployment-specific configuration and real-provider checks; automated regressions do not certify production readiness, target-host capacity, disaster recovery or all mobile browsers. No community desktop installer, automatic AI code commits, subtask decomposition or change-impact analysis is delivered.

## 0.1.0-rc1 — 2026-09-09

Initial community prerelease:

- Standalone Vue/Go/SQLite source, synthetic organization and demo members.
- Admin bootstrap alias, mandatory initial password change and loopback setup restriction.
- Requirements, iterations, defects, tests, collaboration and API integrations.
- AI requirement refinement with selective adoption, AI title/test generation and test review.
- Unified colorful AI actions, yellow search highlighting and adaptive number badges.
- Chinese/English getting-started documentation, Apache-2.0 license, contribution/security guides.

Known limitations: no community desktop installer; no automatic AI code commits, subtask decomposition or change-impact analysis. Third-party services need independent configuration. This prerelease is not a full production acceptance certificate.
