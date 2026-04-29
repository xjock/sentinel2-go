# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`sentinel2-go` is a single-file Go CLI that queries the Earth Search STAC API and downloads public Sentinel-2 L2A satellite imagery bands as Cloud Optimized GeoTIFFs. It is pure standard library — zero external Go dependencies.

## Common Commands

| Task | Command |
|------|---------|
| Build binary | `go build -o sentinel2-go main.go` or `make build` |
| Run | `go run main.go -config config.json -dest ./sentinel2_data` or `make run` |
| Format | `go fmt ./...` or `make fmt` |
| Vet | `go vet ./...` or `make vet` |
| Clean | `make clean` (removes binary and `sentinel2_data/`) |
| Docker build | `docker build -t sentinel2-go .` or `make docker` |

There are no Go tests in this repo. CI runs `go build`, `go fmt` check, and `go vet`.

## CLI Flags

- `-config` — path to JSON config file (default: `config.json`)
- `-dest` — destination directory for downloads (default: `./sentinel2_data`)

## Architecture

The entire application lives in `main.go` with no sub-packages.

**Data flow:**
1. `LoadConfig(path)` reads and unmarshals `config.json` into `Config`. Default `limit` is 20 if omitted.
2. `SearchItems(opts)` performs an HTTP GET to `https://earth-search.aws.element84.com/v1/search` with `collections=sentinel-2-l2a`, bbox, datetime range, and limit. Returns `STACItemCollection`.
3. `FilterItemsByCloud(items, maxCloud)` filters the `features` slice by `properties["eo:cloud_cover"]`.
4. `DownloadAsset(asset, destDir, itemID, bandName)` downloads each requested band via `asset.Href`. Skips files that already exist in the destination directory.
5. `BuildRGB(destDir, itemID)` is called after each item’s bands are downloaded. It shells out to `gdalbuildvrt` and `gdal_translate` to produce an RGB composite TIFF from `red`, `green`, and `blue` bands. If GDAL is missing or bands are absent, it logs a warning and continues.

**Key types:**
- `Config` — mirrors the JSON config fields (`bbox`, `start_date`, `end_date`, `max_cloud`, `bands`, `limit`).
- `STACItem` / `STACItemCollection` / `STACProperties` / `Asset` — STAC API response shapes.
- `SearchOptions` — internal struct used to pass query parameters to `SearchItems`.

**Important implementation details:**
- The STAC search timeout is hard-coded to 60 seconds in `SearchItems`.
- `DownloadAsset` uses `http.Get` without a custom timeout.
- Bands are matched by string key against `item.Assets` (e.g., `"red"`, `"nir"`).
- File naming convention: `<item.ID>_<band>.tif`.
- `os/exec` calls to GDAL are only in `BuildRGB`; everything else is standard library HTTP/JSON/file I/O.
