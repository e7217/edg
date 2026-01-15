# Release Infrastructure Testing Guide

## Overview

This guide explains how to test the release-please infrastructure for EDG Platform.

## Testing Steps

### 1. Local Version Information Test

First, verify that version information is properly embedded in the binary:

```bash
# Build locally with version info
go build \
  -ldflags="-X main.Version=v0.1.0-test \
            -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
            -X main.GitCommit=$(git rev-parse HEAD)" \
  -o edg-core ./cmd/core

# Test version flag
./edg-core --version
```

**Expected Output:**
```
EDG Platform Core
Version:    v0.1.0-test
Build Time: 2024-01-15T10:30:00Z
Git Commit: abc123...
```

### 2. Create a Test Commit with Conventional Commit Format

Release-please uses Conventional Commits to automatically generate releases. Create a test commit:

```bash
# Make a small change (e.g., update README)
echo "# Testing release-please" >> README.md

# Commit with conventional commit format
git add README.md
git commit -m "feat: add release-please automation

- Automated CHANGELOG generation
- Version management with semantic versioning
- GitHub Release creation"

# Push to main branch
git push origin main
```

### 3. Verify Release-Please PR Creation

After pushing to main, release-please workflow will:

1. **Analyze commits** using Conventional Commits format
2. **Create/update Release PR** with:
   - Updated CHANGELOG.md
   - Version bump in `.release-please-manifest.json`
   - Release notes

**Check the PR:**
```bash
# List open PRs
gh pr list

# View the release PR
gh pr view <PR_NUMBER>
```

**Expected Release PR contents:**
- Title: `chore(main): release 0.1.0`
- Updated `CHANGELOG.md` with commit messages
- Updated `.release-please-manifest.json` with new version

### 4. Merge Release PR to Trigger Release

When you merge the Release PR:

1. **release-please** creates a Git tag (e.g., `v0.1.0`)
2. **release.yml** workflow triggers on the tag
3. **Artifacts are built** for all platforms:
   - linux/amd64
   - linux/arm64
   - darwin/amd64
   - windows/amd64

**Monitor the release:**
```bash
# Check workflow status
gh run list --workflow=release.yml

# View specific run
gh run view <RUN_ID>

# Check releases
gh release list
```

### 5. Validate Release Artifacts

Once the release is published, verify artifacts:

```bash
# List release assets
gh release view v0.1.0

# Download and test an artifact (Linux AMD64)
gh release download v0.1.0 --pattern "*linux-amd64.tar.gz"

# Extract and test
tar -xzf edg-v0.1.0-linux-amd64.tar.gz
cd edg-v0.1.0-linux-amd64
./edg-core --version
```

**Expected artifacts:**
- `edg-v0.1.0-linux-amd64.tar.gz`
- `edg-v0.1.0-linux-arm64.tar.gz`
- `edg-v0.1.0-darwin-amd64.tar.gz`
- `edg-v0.1.0-windows-amd64.zip`

Each artifact should contain:
- `edg-core` (or `edg-core.exe` for Windows)
- `telegraf` binary
- `victoria-metrics-prod` binary
- `configs/` directory
- `install.sh` script
- `README.md`
- `THIRD_PARTY_LICENSES.md`

### 6. Test Version Information in Release Build

Extract a release artifact and verify version information:

```bash
# Should show the actual release version
./edg-core --version

# Expected output:
# EDG Platform Core
# Version:    v0.1.0
# Build Time: 2024-01-15T11:00:00Z
# Git Commit: def456...
```

## Conventional Commit Types

Release-please recognizes these commit types:

- **feat**: A new feature (bumps MINOR version)
- **fix**: A bug fix (bumps PATCH version)
- **docs**: Documentation only changes
- **style**: Code style changes (formatting, etc.)
- **refactor**: Code refactoring
- **perf**: Performance improvements
- **test**: Adding or updating tests
- **build**: Build system changes
- **ci**: CI configuration changes
- **chore**: Other changes that don't modify src or test files

**Breaking changes** (bumps MAJOR version):
```bash
git commit -m "feat!: redesign API endpoints

BREAKING CHANGE: API endpoints have been restructured"
```

## Troubleshooting

### Release-Please PR Not Created

1. Check workflow runs: `gh run list --workflow=release-please.yml`
2. Verify commits use Conventional Commit format
3. Check that commits are on the `main` branch

### Release Workflow Failed

1. Check workflow logs: `gh run view <RUN_ID> --log-failed`
2. Common issues:
   - Missing dependencies in `deps-v1` release
   - Build failures (check Go version compatibility)
   - Permission issues (check `GITHUB_TOKEN` permissions)

### Version Information Not Showing

1. Verify ldflags are correct in `release.yml`
2. Check that variables are defined in `cmd/core/main.go`
3. Rebuild with explicit version: `go build -ldflags="-X main.Version=test"`

## Configuration Files Reference

### `.github/workflows/release-please.yml`
Manages Release PR creation and GitHub Release publishing

### `release-please-config.json`
Configures release-please behavior (release type, changelog format, etc.)

### `.release-please-manifest.json`
Tracks current version state

### `.github/workflows/release.yml`
Builds and packages release artifacts for all platforms

## Next Steps

After successful testing:

1. ✅ Close issue #29 with reference to test results
2. ✅ Document release process in project wiki
3. ✅ Update contributor guidelines with Conventional Commit requirements
4. ✅ Consider adding pre-commit hooks for commit message validation
