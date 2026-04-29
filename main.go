package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var (
	EarthSearchURL  = "https://earth-search.aws.element84.com/v1"
	Collection      = "sentinel-2-l2a"
	DownloadTimeout = 10 * time.Minute
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

type Config struct {
	BBox       []float64 `json:"bbox"`
	StartDate  string    `json:"start_date"`
	EndDate    string    `json:"end_date"`
	MaxCloud   float64   `json:"max_cloud"`
	Bands      []string  `json:"bands"`
	Limit      int       `json:"limit"`
	MaxWorkers int       `json:"max_workers"`
}

type SearchOptions struct {
	Bbox      []float64
	StartDate string
	EndDate   string
	Limit     int
	MaxCloud  float64
}

type downloadTask struct {
	itemID  string
	band    string
	asset   Asset
	destDir string
}

type downloadResult struct {
	path    string
	err     error
	skipped bool
	task    downloadTask
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	if cfg.Limit == 0 {
		cfg.Limit = 20
	}
	if cfg.MaxWorkers == 0 {
		cfg.MaxWorkers = 4
	}
	return &cfg, nil
}

func SearchItems(opts SearchOptions) (*STACItemCollection, error) {
	if opts.Limit == 0 {
		opts.Limit = 10
	}
	bboxStr := fmt.Sprintf("%f,%f,%f,%f", opts.Bbox[0], opts.Bbox[1], opts.Bbox[2], opts.Bbox[3])
	datetime := fmt.Sprintf("%sT00:00:00Z/%sT23:59:59Z", opts.StartDate, opts.EndDate)

	u, err := url.Parse(EarthSearchURL + "/search")
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	q := u.Query()
	q.Set("collections", Collection)
	q.Set("bbox", bboxStr)
	q.Set("datetime", datetime)
	q.Set("limit", fmt.Sprintf("%d", opts.Limit))
	if opts.MaxCloud > 0 {
		q.Set("query", fmt.Sprintf(`{"eo:cloud_cover":{"lte":%f}}`, opts.MaxCloud))
	}
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

func assetExists(destDir, itemID, bandName string) bool {
	filename := fmt.Sprintf("%s_%s.tif", itemID, bandName)
	_, err := os.Stat(filepath.Join(destDir, filename))
	return err == nil
}

func DownloadAsset(asset Asset, destDir string, itemID string, bandName string) (string, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	filename := fmt.Sprintf("%s_%s.tif", itemID, bandName)
	destPath := filepath.Join(destDir, filename)

	client := &http.Client{Timeout: DownloadTimeout}
	resp, err := client.Get(asset.Href)
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

func downloadWorker(tasks <-chan downloadTask, results chan<- downloadResult) {
	for task := range tasks {
		if assetExists(task.destDir, task.itemID, task.band) {
			path := filepath.Join(task.destDir, fmt.Sprintf("%s_%s.tif", task.itemID, task.band))
			results <- downloadResult{path: path, skipped: true, task: task}
			continue
		}
		path, err := DownloadAsset(task.asset, task.destDir, task.itemID, task.band)
		results <- downloadResult{path: path, err: err, task: task}
	}
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

func BuildRGB(destDir string, itemID string) error {
	bands := []string{"red", "green", "blue"}
	bandPaths := []string{}
	for _, band := range bands {
		p := filepath.Join(destDir, fmt.Sprintf("%s_%s.tif", itemID, band))
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("missing band %s: %w", band, err)
		}
		bandPaths = append(bandPaths, p)
	}

	rgbName := fmt.Sprintf("%s_RGB.tif", itemID)
	rgbPath := filepath.Join(destDir, rgbName)
	if _, err := os.Stat(rgbPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", rgbName)
		return nil
	}

	vrtPath := filepath.Join(destDir, fmt.Sprintf("%s_rgb.vrt", itemID))

	buildCmd := exec.Command("gdalbuildvrt", append([]string{"-separate", vrtPath}, bandPaths...)...)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("gdalbuildvrt failed: %w", err)
	}
	defer os.Remove(vrtPath)

	transCmd := exec.Command("gdal_translate", "-of", "GTiff", vrtPath, rgbPath)
	transCmd.Stdout = os.Stdout
	transCmd.Stderr = os.Stderr
	if err := transCmd.Run(); err != nil {
		return fmt.Errorf("gdal_translate failed: %w", err)
	}

	fmt.Printf("  [rgb] %s\n", rgbPath)
	return nil
}

func main() {
	configPath := flag.String("config", "config.json", "Path to configuration JSON file")
	destDir := flag.String("dest", "./sentinel2_data", "Destination directory for downloaded files")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	opts := SearchOptions{
		Bbox:      cfg.BBox,
		StartDate: cfg.StartDate,
		EndDate:   cfg.EndDate,
		Limit:     cfg.Limit,
		MaxCloud:  cfg.MaxCloud,
	}

	fmt.Printf("Searching Sentinel-2 L2A data...\n")
	fmt.Printf("  Config:  %s\n", *configPath)
	fmt.Printf("  Dest:    %s\n", *destDir)
	fmt.Printf("  BBox:    %v (west, south, east, north)\n", opts.Bbox)
	fmt.Printf("  Date:    %s to %s\n", opts.StartDate, opts.EndDate)
	fmt.Printf("  Cloud:   <= %.0f%%\n", opts.MaxCloud)
	fmt.Printf("  Bands:   %v\n", cfg.Bands)
	fmt.Printf("  Workers: %d\n\n", cfg.MaxWorkers)

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

	tasks := make(chan downloadTask, cfg.MaxWorkers*2)
	results := make(chan downloadResult, cfg.MaxWorkers*2)

	var wg sync.WaitGroup
	for i := 0; i < cfg.MaxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			downloadWorker(tasks, results)
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	fmt.Println("\n=== Downloading Bands ===")
	total := 0
	for _, item := range items {
		fmt.Printf("\nItem: %s\n", item.ID)
		for _, band := range cfg.Bands {
			asset, ok := item.Assets[band]
			if !ok {
				fmt.Printf("  [warn] band '%s' not available\n", band)
				continue
			}
			tasks <- downloadTask{itemID: item.ID, band: band, asset: asset, destDir: *destDir}
			total++
		}
		if err := BuildRGB(*destDir, item.ID); err != nil {
			fmt.Fprintf(os.Stderr, "  [rgb skip] %v\n", err)
		}
	}
	close(tasks)

	failed := 0
	skipped := 0
	for res := range results {
		if res.skipped {
			fmt.Printf("  [skip] %s_%s.tif already exists\n", res.task.itemID, res.task.band)
			skipped++
		} else if res.err != nil {
			fmt.Fprintf(os.Stderr, "  [error] %s/%s: %v\n", res.task.itemID, res.task.band, res.err)
			failed++
		} else {
			fmt.Printf("  [saved] %s\n", filepath.Base(res.path))
		}
	}

	fmt.Println("\nDone.")
	if failed > 0 {
		fmt.Printf("%d/%d downloads failed.\n", failed, total)
		os.Exit(1)
	}
	if skipped > 0 {
		fmt.Printf("%d/%d already existed, skipped.\n", skipped, total)
	}
}
