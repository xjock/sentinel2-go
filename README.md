# Sentinel-2 Go Fetcher

A lightweight Go program to search and download Sentinel-2 L2A satellite imagery from multiple STAC APIs, with an interactive web-based setup wizard.

## Features

- **Multiple data sources**: Earth Search (public, no auth), CDSE STAC (per-band), or CDSE OData (full-scene ZIP)
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
| **CDSE STAC API** | Copernicus Data Space Ecosystem, per-band COG download | Username+Password |
| **CDSE OData API** | Copernicus Data Space Ecosystem, full-scene ZIP download | Username+Password |
| **Custom STAC** | Any compatible STAC API endpoint | none |

### CDSE Setup Steps

1. Visit [dataspace.copernicus.eu](https://dataspace.copernicus.eu/) and register
2. Verify your email
3. In the setup page, enter your CDSE username (email) and password
4. Save and continue

Settings are stored in `~/.sentinel2-go/settings.json` (permissions `0600`).

### Data Source Comparison

| Dimension | Earth Search STAC | CDSE STAC | CDSE OData |
|-----------|-------------------|-----------|------------|
| **Download granularity** | Per-band COG (50–200 MB/band) | Per-band COG (50–200 MB/band) | Full-scene ZIP (500 MB–1 GB+) |
| **Authentication** | None | CDSE account required | CDSE account required |
| **Speed** | Fast (AWS CloudFront CDN) | Medium (EU direct) | Slow (on-the-fly packaging + large files) |
| **Access from China** | May require VPN | Likely accessible without VPN | Likely accessible without VPN |
| **Best for** | Quick preview, on-demand bands, RGB composite | Official source, precise bands, no-VPN fallback | Complete original product package with metadata |
| **Resume support** | Yes | Yes | Yes |
| **RGB composite** | Auto | Auto | N/A (process after extracting ZIP) |

**Recommendation:**

- **Good network, want speed** → Earth Search STAC (default, fastest)
- **Earth Search unreachable, or need official source** → CDSE STAC (per-band, faster than OData)
- **Need complete product ZIP (all bands + metadata)** → CDSE OData (slow but complete)

### `settings.json` — Authentication

```json
{
  "source": "cdse",
  "stac_url": "https://stac.dataspace.copernicus.eu/v1",
  "collection": "sentinel-2-l2a",
  "auth": {
    "username": "your-email@example.com",
    "password": "your-password"
  }
}
```

| Field | Description |
|-------|-------------|
| `username` | CDSE login email |
| `password` | CDSE login password |

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
    "username": "${CDSE_USERNAME}",
    "password": "${CDSE_PASSWORD}"
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

### STAC mode (Earth Search / CDSE STAC)

Per-band downloads, file naming:
```
sentinel2_data/
  S2A_50TMK_20250105_0_L2A_red.tif
  S2A_50TMK_20250105_0_L2A_green.tif
  S2A_50TMK_20250105_0_L2A_blue.tif
  ...
```

CDSE STAC source files are JPEG 2000 (`.jp2`), but GDAL tools read them transparently. RGB composites are built as 8-bit GeoTIFFs.

### OData mode (CDSE OData)

Full-scene ZIP downloads, file naming:
```
sentinel2_data/
  S2A_T50TMK_20250105T030529_MSIL2A.zip
  ...
```

ZIP packages contain the complete product (all JP2 bands + XML metadata). Extract and process with SNAP, ENVI, etc. RGB composites are **not** auto-generated in OData mode.

## FAQ

**Q: Which data source should I use?**
- **Start with Earth Search**: fastest, AWS CloudFront global CDN, but may be unreachable from some networks
- **If Earth Search fails** → switch to **CDSE STAC**: per-band downloads, smaller files, EU academic site likely accessible without VPN
- **Need the complete original product package** → use **CDSE OData**: full-scene ZIP, slow but complete, also likely accessible without VPN

**Q: Download fails / times out?**
- Earth Search / CDSE STAC: each file is ~50-200MB, default timeout is 10 minutes
- CDSE OData: full-scene ZIPs are typically 500MB–1GB+, single-file timeout is 30 minutes, recommend using on a stable network
- If timeouts are frequent, increase `max_retries` in `config.json` (e.g., set to 3 or 5)

**Q: No items returned?**
- Check that your date range is within the available archive
- Ensure your bbox is within land areas
- Try increasing `max_cloud` or removing the cloud filter
- CDSE data availability may differ from Earth Search

**Q: How do I switch data sources?**
```bash
./sentinel2-go -setup
# Re-select a data source in the web wizard and save
```

You can switch between Earth Search, CDSE STAC, and CDSE OData at any time via `-setup`. Already-downloaded files are never affected.

**Q: Can I use custom STAC APIs?**
Yes. In the setup wizard, choose "Custom STAC API" and provide your endpoint URL and collection name.

## License

MIT
