# Changelog

## [Unreleased] - 2026-05-28

### Fixed

- **Compose parser: Environment variables not extracted** (`registry/compose.go`)
  - The `ParseAndBuild` function was not extracting `environment` variables from docker-compose services
  - Added `parseComposeEnv()` function that handles both map format (`KEY: value`) and list format (`KEY=value`)
  - Environment variables are now passed to K3s pod containers via the `ContainerSpec.Env` field
  - This fixes multi-container challenges where services require configuration (e.g., database credentials)

- **VPN operations missing from state package** (`state/redis.go`)
  - Added `VPNConfig` struct for per-user WireGuard configuration
  - Added `GetVPNConfig`, `SetVPNConfig`, `DeleteVPNConfig` methods
  - Added `GetNextAvailableVPNIP` for IP pool management (10.8.0.2-254)
  - Fixes undefined errors in `vpn.go`

### Documentation

- **Architecture: Environment Variable Handling** (`docs/architecture.md`)
  - Added section explaining how compose environment variables are extracted and passed to pods
  - Added note about CTF platform integration requirements for `containers[]` with `env` field
  - Added example JSON showing how to include environment variables in challenge registration

- **README: Cloud Deployment & CTF Integration** (`README.md`)
  - Added port 5000 (local registry), 3000/4000 (CTF platform) to cloud security group table
  - Added cloud provider specific instructions (AWS, GCP, Azure)
  - Added CTF Platform Integration section with flow diagram
  - Added Multi-Container Challenges example with environment variables
  - Added Challenge Packs database schema example

### Changed

- **CTF Backend Integration** (external)
  - CTF platforms creating challenges via `containers[]` must now include the `env` field for each container
  - Example: `{"name": "db", "image": "...", "ports": [5432], "env": {"POSTGRES_PASSWORD": "..."}}`
