# Release Process

This document describes the release process for tf-version-bump, including how releases are built, signed, and verified.

## Overview

Releases are automated via GitHub Actions using [GoReleaser](https://goreleaser.com/). Each release includes:

- Binary builds for multiple platforms (Linux, macOS, Windows) and architectures (amd64, arm64)
- Linux packages (deb, rpm)
- SHA256 checksums
- SLSA Level 3 provenance attestations for supply chain security

## Creating a Release

1. **Tag the release** following semantic versioning:
   ```bash
   git tag -a v1.0.0 -m "Release v1.0.0"
   git push origin v1.0.0
   ```

2. **GitHub Actions automatically**:
   - Builds binaries for all platforms
   - Creates archives and Linux packages
   - Generates SHA256 checksums
   - Creates SLSA provenance attestations
   - Publishes the release to GitHub

## Release Artifacts

Each release includes:

| Artifact | Description |
|----------|-------------|
| `tf-version-bump_Linux_x86_64.tar.gz` | Linux AMD64 binary |
| `tf-version-bump_Linux_arm64.tar.gz` | Linux ARM64 binary |
| `tf-version-bump_Darwin_x86_64.tar.gz` | macOS AMD64 binary |
| `tf-version-bump_Darwin_arm64.tar.gz` | macOS ARM64 (Apple Silicon) binary |
| `tf-version-bump_Windows_x86_64.zip` | Windows AMD64 binary |
| `tf-version-bump_Windows_arm64.zip` | Windows ARM64 binary |
| `tf-version-bump_*.deb` | Debian/Ubuntu packages |
| `tf-version-bump_*.rpm` | RHEL/Fedora packages |
| `tf-version-bump-v*.checksums.txt` | SHA256 checksums for all artifacts |
| `tf-version-bump-v*.intoto.jsonl` | SLSA provenance attestation |

## Verifying Releases

### Verify Checksums

Download the checksums file and verify your download:

> **Note:** Replace `1.0.0` in the examples below with your desired version.

```bash
# Download the binary and checksums
curl -LO https://github.com/yesdevnull/tf-version-bump/releases/download/v1.0.0/tf-version-bump_Linux_x86_64.tar.gz
curl -LO https://github.com/yesdevnull/tf-version-bump/releases/download/v1.0.0/tf-version-bump-v1.0.0.checksums.txt

# Verify the checksum
sha256sum -c tf-version-bump-v1.0.0.checksums.txt --ignore-missing
```

### Verify SLSA Provenance

The release includes SLSA Level 3 provenance attestations that can be verified using the [slsa-verifier](https://github.com/slsa-framework/slsa-verifier):

```bash
# Install slsa-verifier
go install github.com/slsa-framework/slsa-verifier/v2/cli/slsa-verifier@latest

# Download the artifact and provenance
curl -LO https://github.com/yesdevnull/tf-version-bump/releases/download/v1.0.0/tf-version-bump_Linux_x86_64.tar.gz
curl -LO https://github.com/yesdevnull/tf-version-bump/releases/download/v1.0.0/tf-version-bump-v1.0.0.intoto.jsonl

# Verify provenance
slsa-verifier verify-artifact tf-version-bump_Linux_x86_64.tar.gz \
  --provenance-path tf-version-bump-v1.0.0.intoto.jsonl \
  --source-uri github.com/yesdevnull/tf-version-bump \
  --source-tag v1.0.0
```

## Security

### Supply Chain Security

This project uses several measures to ensure supply chain security:

1. **SLSA Level 3 Provenance**: Cryptographically signed attestations that prove where and how artifacts were built
2. **Pinned Dependencies**: All GitHub Actions are pinned to specific commit SHAs
3. **Minimal Permissions**: Workflows use least-privilege permission model

### Reporting Vulnerabilities

If you discover a security vulnerability, please report it responsibly by emailing the maintainers directly rather than opening a public issue.

## Development Releases

For testing release builds locally:

```bash
# Install goreleaser
go install github.com/goreleaser/goreleaser/v2@latest

# Build a snapshot release (no publishing)
goreleaser release --snapshot --clean

# Check the dist/ directory for built artifacts
ls -la dist/
```

## Configuration

Release configuration is defined in:

- `.goreleaser.yaml` - GoReleaser build configuration
- `.github/workflows/release.yml` - GitHub Actions workflow

## Troubleshooting

### Common Issues

**Build fails with "dirty" error**:
Ensure all changes are committed before tagging:
```bash
git status  # Should show clean working tree
git tag v1.0.0
```

**Checksum verification fails**:
Re-download both the artifact and checksums file. Ensure you're using the correct version.

**SLSA verification fails**:
Ensure you have the correct provenance file for the specific version and that the source URI matches.
