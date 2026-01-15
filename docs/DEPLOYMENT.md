# EDG Deployment Guide

## Binary Release

EDG is distributed as pre-built binaries for multiple platforms.

### Supported Platforms

| OS | Architecture | File |
|----|--------------|------|
| Linux | amd64 | `edg-vX.Y.Z-linux-amd64.tar.gz` |
| Linux | arm64 | `edg-vX.Y.Z-linux-arm64.tar.gz` |
| macOS | amd64 | `edg-vX.Y.Z-darwin-amd64.tar.gz` |
| Windows | amd64 | `edg-vX.Y.Z-windows-amd64.zip` |

### Installation

1. Download the release for your platform from [GitHub Releases](https://github.com/e7217/edg/releases)

2. Extract the archive:
   ```bash
   tar -xzf edg-vX.Y.Z-linux-amd64.tar.gz
   cd edg-vX.Y.Z-linux-amd64
   ```

3. Run the installer:
   ```bash
   sudo ./install.sh
   ```

4. Start services:
   ```bash
   sudo systemctl start edg-victoriametrics
   sudo systemctl start edg-core
   sudo systemctl start edg-telegraf
   ```

### Manual Installation

If you prefer manual installation:

```bash
# Copy binaries
sudo mkdir -p /opt/edg/bin
sudo cp edg-core telegraf victoria-metrics-prod /opt/edg/bin/

# Copy configs
sudo mkdir -p /opt/edg/configs
sudo cp -r configs/* /opt/edg/configs/

# Create data directory
sudo mkdir -p /opt/edg/data
```

## Creating a Release

Releases are created automatically when a version tag is pushed:

```bash
# Create and push a version tag
git tag v1.0.0
git push origin v1.0.0
```

The [release workflow](../.github/workflows/release.yml) will:
1. Build `edg-core` for all platforms
2. Download Telegraf and VictoriaMetrics from `deps-v1` release
3. Package everything into release archives
4. Upload to GitHub Releases

## Docker (Development)

For development, use Docker Compose:

```bash
cd deploy/docker
docker compose up -d
```

See [deploy/docker/README.md](../deploy/docker/README.md) for details.
