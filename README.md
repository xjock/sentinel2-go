# Sentinel-2 Go Fetcher

A lightweight Go program to search and download public Sentinel-2 L2A satellite imagery from [Earth Search](https://earth-search.aws.element84.com/) STAC API without any authentication.

## Features

- No API keys or credentials required
- Query by bounding box, date range, and cloud cover
- Download individual bands as Cloud Optimized GeoTIFF (COG)
- Skip already-downloaded files
- Pure Go, zero external dependencies
- Configuration via JSON file
- Command-line argument for output directory

## Prerequisites

- [Go](https://go.dev/) 1.21 or later

## Quick Start

```bash
git clone <your-repo-url>
cd sentinel2-go

# Edit config.json for your area and dates
go run main.go -config config.json -dest ./sentinel2_data
```

The program will:
1. Load search parameters from `config.json`
2. Search Sentinel-2 L2A data for the configured bounding box and date range
3. Filter results by cloud cover
4. Download the requested bands to the specified destination directory

## Configuration

Create a `config.json` file (see `config.json.example`):

```json
{
  "bbox": [116.2, 39.8, 116.6, 40.0],
  "start_date": "2025-01-01",
  "end_date": "2025-01-15",
  "max_cloud": 20.0,
  "bands": ["red", "green", "blue", "nir"],
  "limit": 20
}
```

| Field | Type | Description |
|-------|------|-------------|
| `bbox` | `[float64]` | Bounding box `[west, south, east, north]` |
| `start_date` | `string` | Start date `YYYY-MM-DD` |
| `end_date` | `string` | End date `YYYY-MM-DD` |
| `max_cloud` | `float64` | Maximum cloud cover percentage (0-100) |
| `bands` | `[string]` | List of bands to download |
| `limit` | `int` | Max number of STAC items to query (default: 20) |

### Command-line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `config.json` | Path to configuration JSON file |
| `-dest` | `./sentinel2_data` | Destination directory for downloaded files |

### Examples

```bash
# Use default config.json and default dest directory
go run main.go

# Use custom config and output directory
go run main.go -config beijing.json -dest /data/s2_beijing

# Run compiled binary
./sentinel2-go -config europe.json -dest ./europe_s2
```

### Getting Bounding Box

- [geojson.io](http://geojson.io/) — draw a rectangle and read coordinates
- Python: `from shapely import box; list(box(minx, miny, maxx, maxy).bounds)`

### Available Bands

| Band Name | Description |
|-----------|-------------|
| `coastal` | B01 Coastal / Aerosol |
| `blue`    | B02 Blue |
| `green`   | B03 Green |
| `red`     | B04 Red |
| `rededge1`| B05 Red Edge 1 |
| `rededge2`| B06 Red Edge 2 |
| `rededge3`| B07 Red Edge 3 |
| `nir`     | B08 Near Infrared |
| `nir08`   | B8A Narrow NIR |
| `nir09`   | B09 Water Vapor |
| `swir16`  | B11 SWIR 1 |
| `swir22`  | B12 SWIR 2 |
| `scl`     | Scene Classification |

## Build

```bash
# Build binary
go build -o sentinel2-go main.go

# Run binary
./sentinel2-go -config config.json -dest ./output
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

These are standard GeoTIFFs and can be opened with QGIS, GDAL, Python (`rioxarray`), etc.

## FAQ

**Q: Download fails / times out?**
- Each COG file is approximately 50-200MB, download time depends on your network
- Default HTTP timeout is 5 minutes, adjust in `DownloadAsset` if needed

**Q: No items returned?**
- Check that your date range is within the available archive (Earth Search generally keeps recent years)
- Ensure your bbox is within land areas
- Try increasing `max_cloud` or removing the cloud filter

**Q: Read pixel values instead of downloading files?**
- This program only downloads files
- To read TIFF pixels in Go, use `github.com/airbusgeo/godal` (GDAL Go bindings), but this requires installing GDAL C libraries

## License

MIT
