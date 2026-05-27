# Changelog

## [Unreleased] - 2026-05-28

### Fixed

- **Compose parser: Environment variables not extracted** (`registry/compose.go`)
  - The `ParseAndBuild` function was not extracting `environment` variables from docker-compose services
  - Added `parseComposeEnv()` function that handles both map format (`KEY: value`) and list format (`KEY=value`)
  - Environment variables are now passed to K3s pod containers via the `ContainerSpec.Env` field
  - This fixes multi-container challenges where services require configuration (e.g., database credentials)

### Documentation

- **Architecture: Environment Variable Handling** (`docs/architecture.md`)
  - Added section explaining how compose environment variables are extracted and passed to pods
  - Added note about CTF platform integration requirements for `containers[]` with `env` field
  - Added example JSON showing how to include environment variables in challenge registration

### Changed

- **CTF Backend Integration** (external)
  - CTF platforms creating challenges via `containers[]` must now include the `env` field for each container
  - Example: `{"name": "db", "image": "...", "ports": [5432], "env": {"POSTGRES_PASSWORD": "..."}}`
