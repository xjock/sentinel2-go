# Sentinel-2 Go Fetcher

A lightweight Go program to search and download Sentinel-2 L2A satellite imagery from multiple STAC APIs, with an interactive web-based setup wizard.

## Features

- **Multiple data sources**: Earth Search (public, no auth) or Copernicus Data Space Ecosystem (CDSE)
- **Web setup wizard**: First run automatically opens a browser page for configuration
- **Automatic band mapping**: Use friendly names like `red`, `green`, `blue` — automatically translated to provider-specific keys
- **Resume support**: Skip already-downloaded files, resume interrupted downloads
- **Concurrent downloads**: Configurable worker pool
- **RGB composite**: Auto-build RGB TIFFs via GDAL
- **Pure Go, zero external dependencies**

## Quick Start

```bash
git clone <your-repo-url>
cd sentinel2-go
go build -o sentinel2-go main.go

# First run — automatically opens browser setup page
./sentinel2-go
```

On first run, a browser page opens at `http://127.0.0.1:<random-port>`. Choose your data source and authentication method, then the program continues automatically.

## Setup Wizard

### First Run (Auto)

```bash
./sentinel2-go
```

If `~/.sentinel2-go/settings.json` does not exist, the program automatically starts a local HTTP server and opens your browser.

### Manual Reconfiguration

```bash
# Web setup (with browser)
./sentinel2-go -setup

# Terminal setup (no browser, SSH-friendly)
./sentinel2-go -setup-auth
```

### Data Source Options

| Option | Description | Authentication |
|--------|-------------|----------------|
| **Earth Search STAC API** | Public AWS-hosted STAC, no auth needed | None |
| **CDSE** | Copernicus Data Space Ecosystem, official EU source | Username+Password or OAuth Client Credentials |
| **Custom STAC** | Any compatible STAC API endpoint | OAuth2 or none |

### CDSE Setup Steps

1. Visit [dataspace.copernicus.eu](https://dataspace.copernicus.eu/) and register
2. Verify your email
3. In the setup page, choose authentication method:
   - **Username + Password** — quick start, uses your CDSE login
   - **OAuth Client Credentials** — recommended for long-term use; create an OAuth Client in Account Settings
4. Save and continue

Settings are stored in `~/.sentinel2-go/settings.json` (permissions `0600`).

### `settings.json` — Authentication

```json
// Username + Password
{
  "source": "cdse",
  "stac_url": "https://stac.dataspace.copernicus.eu/v1",
  "collection": "sentinel-2-l2a",
  "auth": {
    "grant_type": "password",
    "username": "your-email@example.com",
    "password": "your-password"
  }
}

// OAuth Client Credentials
{
  "source": "cdse",
  "stac_url": "https://stac.dataspace.copernicus.eu/v1",
  "collection": "sentinel-2-l2a",
  "auth": {
    "grant_type": "client_credentials",
    "client_id": "your-client-id",
    "client_secret": "your-client-secret"
  }
}
```

| Field | Required for | Description |
|-------|--------------|-------------|
| `grant_type` | all CDSE | `"password"` or `"client_credentials"` |
| `username` | password | CDSE login email |
| `password` | password | CDSE login password |
| `client_id` | client_credentials | OAuth Client ID from Account Settings |
| `client_secret` | client_credentials | OAuth Client Secret |

## Configuration

### `config.json` — Query Parameters

```json
{
  "bbox": [116.2, 39.8, 116.6, 40.0],
  "start_date": "2026-04-01",
  "end_date": "2026-04-15",
  "max_cloud": 20.0,
  "bands": ["red", "green", "blue", "nir"],
  "limit": 20,
  "max_workers": 4,
  "max_retries": 3
}
```

| Field | Type | Description |
|-------|------|-------------|
| `bbox` | `[float64]` | Bounding box `[west, south, east, north]` |
| `start_date` | `string` | Start date `YYYY-MM-DD` |
| `end_date` | `string` | End date `YYYY-MM-DD` |
| `max_cloud` | `float64` | Maximum cloud cover percentage (0-100) |
| `bands` | `[string]` | List of bands to download (friendly names) |
| `limit` | `int` | Max number of STAC items to query (default: 20) |
| `max_workers` | `int` | Concurrent download workers (default: 4) |
| `max_retries` | `int` | Retry attempts per failed download (default: 0) |

### Command-line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `config.json` | Path to query configuration JSON |
| `-dest` | `./sentinel2_data` | Destination directory |
| `-setup` | — | Open web setup wizard |
| `-setup-auth` | — | Terminal authentication setup |

### Environment Variables

In `config.json`, you can reference environment variables:

```json
{
  "auth": {
    "client_id": "${CDSE_CLIENT_ID}",
    "client_secret": "${CDSE_CLIENT_SECRET}"
  }
}
```

## Band Mapping

Use **friendly names** in `config.json`. The program automatically maps them to provider-specific asset keys.

### Earth Search Bands

| Friendly Name | Earth Search Key | Sentinel-2 Band |
|---------------|------------------|-----------------|
| `coastal` | `coastal` | B01 |
| `blue` | `blue` | B02 |
| `green` | `green` | B03 |
| `red` | `red` | B04 |
| `rededge1` | `rededge1` | B05 |
| `rededge2` | `rededge2` | B06 |
| `rededge3` | `rededge3` | B07 |
| `nir` | `nir` | B08 |
| `nir08` | `nir08` | B8A |
| `nir09` | `nir09` | B09 |
| `swir16` | `swir16` | B11 |
| `swir22` | `swir22` | B12 |
| `scl` | `scl` | SCL |

### CDSE Bands (Auto-mapped)

| Friendly Name | CDSE Asset Key | Resolution |
|---------------|----------------|------------|
| `coastal` | `B01_60m` | 60m |
| `blue` | `B02_10m` | 10m |
| `green` | `B03_10m` | 10m |
| `red` | `B04_10m` | 10m |
| `rededge1` | `B05_20m` | 20m |
| `rededge2` | `B06_20m` | 20m |
| `rededge3` | `B07_20m` | 20m |
| `nir` | `B08_10m` | 10m |
| `nir08` | `B8A_20m` | 20m |
| `nir09` | `B09_60m` | 60m |
| `swir16` | `B11_20m` | 20m |
| `swir22` | `B12_20m` | 20m |
| `scl` | `SCL_20m` | 20m |
| `aot` | `AOT_20m` | 20m |
| `wvp` | `WVP_10m` | 10m |
| `tci` | `TCI_10m` | 10m |

Example: if you set `"bands": ["red", "green", "blue"]`, the program automatically downloads `B04_10m`, `B03_10m`, `B02_10m` from CDSE, but saves them as `<item>_red.tif`, `<item>_green.tif`, `<item>_blue.tif` for compatibility with `BuildRGB`.

## Build

```bash
go build -o sentinel2-go main.go
```

## Docker

```bash
docker build -t sentinel2-go .
docker run --rm -v $(pwd)/config.json:/app/config.json -v $(pwd)/sentinel2_data:/app/sentinel2_data sentinel2-go
```

## Output

Downloaded files are named as:
```
sentinel2_data/
  S2A_50TMK_20250105_0_L2A_red.tif
  S2A_50TMK_20250105_0_L2A_green.tif
  S2A_50TMK_20250105_0_L2A_blue.tif
  ...
```

For CDSE, the source files are JPEG 2000 (`.jp2`), but GDAL tools handle them transparently. RGB composites are built as 8-bit GeoTIFFs.

## FAQ

**Q: Download fails / times out?**
- Each file is approximately 50-200MB, download time depends on your network
- Default timeout is 10 minutes
- CDSE OData downloads may be slower than Earth Search COGs

**Q: No items returned?**
- Check that your date range is within the available archive
- Ensure your bbox is within land areas
- Try increasing `max_cloud` or removing the cloud filter
- CDSE data availability may differ from Earth Search

**Q: How do I switch from CDSE back to Earth Search?**
```bash
./sentinel2-go -setup
# Select "Earth Search STAC API (no auth)" and save
```

**Q: Can I use custom STAC APIs?**
Yes. In the setup wizard, choose "Custom STAC API" and provide your endpoint URL, collection name, and OAuth credentials.

## License

MIT
