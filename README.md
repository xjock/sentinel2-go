# Sentinel-2 Go Fetcher

A lightweight Go program to search and download public Sentinel-2 L2A satellite imagery from [Earth Search](https://earth-search.aws.element84.com/) STAC API without any authentication.

## Features

- No API keys or credentials required
- Query by bounding box, date range, and cloud cover
- Download individual bands as Cloud Optimized GeoTIFF (COG)
- Skip already-downloaded files
- Pure Go, zero external dependencies

## Prerequisites

- [Go](https://go.dev/) 1.21 or later

## Quick Start

```bash
git clone <your-repo-url>
cd sentinel2-go
go run main.go
```

The program will:
1. Search Sentinel-2 L2A data for the configured bounding box and date range
2. Filter results by cloud cover
3. Download the requested bands to `./sentinel2_data/`

## Configuration

Edit `main.go` and modify the variables at the top of `main()`:

```go
bbox := []float64{116.2, 39.8, 116.6, 40.0}   // west, south, east, north
startDate := "2025-01-01"
endDate   := "2025-01-15"
maxCloud  := 20.0                               // max cloud cover %
bandsToDownload := []string{"red", "green", "blue", "nir"}
destDir := "./sentinel2_data"
```

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
./sentinel2-go
```

## Docker

```bash
docker build -t sentinel2-go .
docker run --rm -v $(pwd)/sentinel2_data:/app/sentinel2_data sentinel2-go
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

## License

MIT
