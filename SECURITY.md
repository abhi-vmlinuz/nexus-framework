# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Nexus OSS, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, email: **abhishekvincent29@outlook.com**

Include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

## Response Timeline

- **Acknowledgment**: within 48 hours
- **Initial assessment**: within 1 week
- **Fix or mitigation**: within 2 weeks for critical/high severity

## Scope

The following components are in scope:
- `nexus-engine` (API server)
- `nexus-cli` (operator CLI)
- `nexus-node-agent` (privileged network daemon)
- `nexus-installer` (TUI installer)
- `bootstrap.sh` (one-command installer)

## Out of Scope

- Third-party dependencies (report to their maintainers)
- Social engineering attacks
- Issues requiring physical access to the server

## Past Security Audits

- **v0.1.2-beta**: Pre-release security audit found and fixed 26 vulnerabilities (3 critical, 5 high, 11 medium, 7 low). All resolved.
