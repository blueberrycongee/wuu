# Security Policy

This document explains which versions receive fixes and how to report security
vulnerabilities in wuu. For the product's file, command, network, model,
credential, and remote-control trust boundaries, read the
[Security Model](docs/en/reference/security-model.md).

## Supported versions

wuu is a preview product using [CalVer](docs/en/project/release.md#version-policy)
and does not promise a fixed support window for older versions. Security fixes
are released on the current release line. Upgrade to
the newest release before reporting an issue that may already be fixed.

## Reporting a vulnerability

Please do **not** open a public issue for security problems.

Report privately through one of the following channels:

1. **GitHub Security Advisories** (preferred): open a private advisory from
   the project's Security tab.
2. **Email**: see the maintainer address in the recent commit history.

Please include:

- A clear description of the issue and its impact
- Reproduction steps, ideally with a minimal repo or transcript
- The affected version (`wuu --version`)
- Any known mitigations or workarounds

Maintainers will coordinate disclosure timing with reporters and credit
reporters who request it. No fixed response or remediation time is promised at
the current project stage.

## Scope

In scope:

- Provider API key handling, `.wuu.json` parsing, and secret persistence
- Tool execution sandboxing and command escaping
- Renderer / main process IPC in the desktop shell
- Network requests, TLS, and proxy handling
- Project instructions, skills, hooks, MCP servers, and remote-control pairing

Out of scope:

- Issues in third-party model providers (report upstream)
- Expected access that is clearly documented for `unconfined` mode
- A model following an instruction that the user explicitly trusted, unless it
  crosses the documented workspace or credential boundary

Security reports are still welcome when the documented boundary is unclear or
the product exposes more data or authority than the UI or documentation says.
