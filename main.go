package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const (
	EarthSearchURL = "https://earth-search.aws.element84.com/v1"
	Collection     = "sentinel-2-l2a"
)

type STACItemCollection struct {
	Type     string     `json:"type"`
	Features []STACItem `json:"features"`
}

type STACItem struct {
	ID         string           `json:"id"`
	Type       string           `json:"type"`
	Collection string           `json:"collection"`
	BBox       []float64        `json:"bbox"`
	Properties STACProperties   `json:"properties"`
	Assets     map[string]Asset `json:"assets"`
}

type STACProperties struct {
	Datetime   string  `json:"datetime"`
	Created    string  `json:"created"`
	CloudCover float64 `json:"eo:cloud_cover"`
	GranuleID  string  `json:"s2:granule_id,omitempty"`
}

type Asset struct {
	Href  string   `json:"href"`
	Type  string   `json:"type"`
	Title string   `json:"title"`
	Roles []string `json:"roles"`
}

type SearchOptions struct {
	Bbox      []float64
	StartDate string
	EndDate   string
	Limit     int
	MaxCloud  float64
}

func SearchItems(opts SearchOptions) (*STACItemCollection, error) {
	if opts.Limit == 0 {
		opts.Limit = 10
	}
	bboxStr := fmt.Sprintf("%f,%f,%f,%f", opts.Bbox[0], opts.Bbox[1], opts.Bbox[2], opts.Bbox[3])
	datetime := fmt.Sprintf("%sT00:00:00Z/%sT23:59:59Z", opts.StartDate, opts.EndDate)

	u, _ := url.Parse(EarthSearchURL + "/search")
	q := u.Query()
	q.Set("collections", Collection)
	q.Set("bbox", bboxStr)
	q.Set("datetime", datetime)
	q.Set("limit", fmt.Sprintf("%d", opts.Limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/geo+json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("STAC API returned %d: %s", resp.StatusCode, string(body))
	}

	var collection STACItemCollection
	if err := json.NewDecoder(resp.Body).Decode(&collection); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	return &collection, nil
}

func FilterItemsByCloud(items []STACItem, maxCloud float64) []STACItem {
	var filtered []STACItem
	for _, item := range items {
		if item.Properties.CloudCover <= maxCloud {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func DownloadAsset(asset Asset, destDir string, itemID string, bandName string) (string, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	filename := fmt.Sprintf("%s_%s.tif", itemID, bandName)
	destPath := filepath.Join(destDir, filename)

	if _, err := os.Stat(destPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", filename)
		return destPath, nil
	}

	fmt.Printf("  [downloading] %s -> %s\n", bandName, filename)
	resp, err := http.Get(asset.Href)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, asset.Href)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	if err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return destPath, nil
}

func PrintItemSummary(items []STACItem) {
	fmt.Println("\n=== Found Items ===")
	for _, item := range items {
		dt := item.Properties.Datetime
		if dt == "" {
			dt = item.Properties.Created
		}
		fmt.Printf("- %s | Date: %s | Cloud: %.1f%% | BBox: %v\n",
			item.ID, dt, item.Properties.CloudCover, item.BBox)
	}
}

func main() {
	bbox := []float64{116.2, 39.8, 116.6, 40.0}
	startDate := "2025-01-01"
	endDate := "2025-01-15"
	maxCloud := 20.0
	bandsToDownload := []string{"red", "green", "blue", "nir"}
	destDir := "./sentinel2_data"

	opts := SearchOptions{
		Bbox:      bbox,
		StartDate: startDate,
		EndDate:   endDate,
		Limit:     20,
		MaxCloud:  maxCloud,
	}

	fmt.Printf("Searching Sentinel-2 L2A data...\n")
	fmt.Printf("  BBox: %v (west, south, east, north)\n", opts.Bbox)
	fmt.Printf("  Date: %s to %s\n", opts.StartDate, opts.EndDate)
	fmt.Printf("  Max Cloud: %.0f%%\n\n", opts.MaxCloud)

	collection, err := SearchItems(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Search failed: %v\n", err)
		os.Exit(1)
	}

	if len(collection.Features) == 0 {
		fmt.Println("No items found.")
		return
	}

	items := FilterItemsByCloud(collection.Features, opts.MaxCloud)
	PrintItemSummary(items)

	fmt.Println("\n=== Downloading Bands ===")
	for _, item := range items {
		fmt.Printf("\nItem: %s\n", item.ID)
		for _, band := range bandsToDownload {
			asset, ok := item.Assets[band]
			if !ok {
				fmt.Printf("  [warn] band '%s' not available\n", band)
				continue
			}
			path, err := DownloadAsset(asset, destDir, item.ID, band)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  [error] %s: %v\n", band, err)
				continue
			}
			fmt.Printf("  [saved] %s\n", path)
		}
	}
	fmt.Println("\nDone.")
}
