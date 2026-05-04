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
	"strconv"
	"strings"
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

type Geometry struct {
	Type        string          `json:"type"`
	Coordinates [][][]float64   `json:"coordinates"`
}

type STACItem struct {
	ID         string           `json:"id"`
	Type       string           `json:"type"`
	Collection string           `json:"collection"`
	BBox       []float64        `json:"bbox"`
	Geometry   Geometry         `json:"geometry"`
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
	MaxRetries int       `json:"max_retries"`
}

type SearchOptions struct {
	Bbox      []float64
	StartDate string
	EndDate   string
	Limit     int
	MaxCloud  float64
}

type downloadTask struct {
	itemID     string
	band       string
	asset      Asset
	destDir    string
	maxRetries int
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
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
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

func SaveKML(item STACItem, destDir string) (string, error) {
	if item.Geometry.Type != "Polygon" || len(item.Geometry.Coordinates) == 0 {
		return "", fmt.Errorf("no polygon geometry for %s", item.ID)
	}

	kmlPath := filepath.Join(destDir, item.ID+".kml")
	if _, err := os.Stat(kmlPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", item.ID+".kml")
		return kmlPath, nil
	}

	ring := item.Geometry.Coordinates[0]
	var coords strings.Builder
	for _, p := range ring {
		if len(p) >= 2 {
			coords.WriteString(fmt.Sprintf("%f,%f,0 ", p[0], p[1]))
		}
	}

	kml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2">
  <Document>
    <Style id="polyStyle">
      <LineStyle>
        <color>ff0000ff</color>
        <width>2</width>
      </LineStyle>
      <PolyStyle>
        <color>7f0000ff</color>
        <fill>1</fill>
        <outline>1</outline>
      </PolyStyle>
    </Style>
    <Placemark>
      <name>%s</name>
      <styleUrl>#polyStyle</styleUrl>
      <Polygon>
        <outerBoundaryIs>
          <LinearRing>
            <coordinates>%s</coordinates>
          </LinearRing>
        </outerBoundaryIs>
      </Polygon>
    </Placemark>
  </Document>
</kml>`, item.ID, strings.TrimSpace(coords.String()))

	if err := os.WriteFile(kmlPath, []byte(kml), 0644); err != nil {
		return "", fmt.Errorf("write kml: %w", err)
	}
	fmt.Printf("  [saved] %s\n", item.ID+".kml")
	return kmlPath, nil
}

var knownBands = []string{"coastal", "blue", "green", "red", "rededge1", "rededge2", "rededge3", "nir", "nir08", "nir09", "swir16", "swir22", "scl"}

func parseItemIDFromFilename(filename string) string {
	if !strings.HasSuffix(filename, ".tif") {
		return ""
	}
	base := strings.TrimSuffix(filename, ".tif")
	for _, band := range knownBands {
		suffix := "_" + band
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix)
		}
	}
	return ""
}

func scanExistingItems(destDir string) map[string]bool {
	items := make(map[string]bool)
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return items
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		itemID := parseItemIDFromFilename(entry.Name())
		if itemID != "" {
			items[itemID] = true
		}
	}
	return items
}

func fetchItemGeometry(itemID string) (Geometry, error) {
	u, err := url.Parse(fmt.Sprintf("%s/collections/%s/items/%s", EarthSearchURL, Collection, itemID))
	if err != nil {
		return Geometry{}, fmt.Errorf("parse URL: %w", err)
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return Geometry{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/geo+json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Geometry{}, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return Geometry{}, fmt.Errorf("STAC API returned %d: %s", resp.StatusCode, string(body))
	}

	var item STACItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return Geometry{}, fmt.Errorf("decode JSON: %w", err)
	}
	if item.Geometry.Type != "Polygon" || len(item.Geometry.Coordinates) == 0 {
		return Geometry{}, fmt.Errorf("no polygon geometry in response")
	}
	return item.Geometry, nil
}

func assetExists(destDir, itemID, bandName string) bool {
	filename := fmt.Sprintf("%s_%s.tif", itemID, bandName)
	_, err := os.Stat(filepath.Join(destDir, filename))
	return err == nil
}

type progressReader struct {
	r           io.Reader
	total       int64
	current     int64
	lastPercent int
	label       string
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.current += int64(n)
	if pr.total > 0 {
		percent := int(pr.current * 100 / pr.total)
		if percent >= pr.lastPercent+10 {
			fmt.Fprintf(os.Stderr, "  [%s] %3d%% (%s / %s)\n", pr.label, percent, formatBytes(pr.current), formatBytes(pr.total))
			pr.lastPercent = percent
		}
	} else {
		// 未知总大小时，每 10 MB 打印一次
		if pr.current >= int64(pr.lastPercent)*10*1024*1024 {
			fmt.Fprintf(os.Stderr, "  [%s] downloaded %s\n", pr.label, formatBytes(pr.current))
			pr.lastPercent++
		}
	}
	return n, err
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func parseContentRangeTotal(contentRange string) int64 {
	idx := strings.LastIndex(contentRange, "/")
	if idx < 0 {
		return 0
	}
	total, _ := strconv.ParseInt(contentRange[idx+1:], 10, 64)
	return total
}

func DownloadAsset(asset Asset, destDir string, itemID string, bandName string) (string, bool, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", false, fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	filename := fmt.Sprintf("%s_%s.tif", itemID, bandName)
	destPath := filepath.Join(destDir, filename)

	var offset int64
	if info, err := os.Stat(destPath); err == nil {
		offset = info.Size()
	}

	client := &http.Client{Timeout: DownloadTimeout}
	req, err := http.NewRequest("GET", asset.Href, nil)
	if err != nil {
		return "", false, fmt.Errorf("create request: %w", err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		total := resp.ContentLength
		if offset > 0 {
			if total > 0 && offset == total {
				resp.Body.Close()
				return destPath, true, nil
			}
			os.Remove(destPath)
			offset = 0
		}
		f, err := os.Create(destPath)
		if err != nil {
			return "", false, fmt.Errorf("create file: %w", err)
		}
		defer f.Close()

		pr := &progressReader{r: resp.Body, total: total, current: 0, label: fmt.Sprintf("%s/%s", itemID, bandName)}
		if total > 0 {
			fmt.Fprintf(os.Stderr, "  [downloading] %s (%s)\n", filename, formatBytes(total))
		} else {
			fmt.Fprintf(os.Stderr, "  [downloading] %s (unknown size)\n", filename)
		}

		_, err = io.Copy(f, pr)
		if err != nil {
			os.Remove(destPath)
			return "", false, fmt.Errorf("write file: %w", err)
		}
		if total > 0 {
			info, err := os.Stat(destPath)
			if err != nil {
				os.Remove(destPath)
				return "", false, fmt.Errorf("stat file: %w", err)
			}
			if info.Size() != total {
				os.Remove(destPath)
				return "", false, fmt.Errorf("size mismatch: got %s, expected %s", formatBytes(info.Size()), formatBytes(total))
			}
		}
		return destPath, false, nil

	case http.StatusPartialContent:
		total := parseContentRangeTotal(resp.Header.Get("Content-Range"))
		if total == 0 {
			total = offset + resp.ContentLength
		}
		f, err := os.OpenFile(destPath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return "", false, fmt.Errorf("open file for append: %w", err)
		}
		defer f.Close()

		pr := &progressReader{r: resp.Body, total: total, current: offset, label: fmt.Sprintf("%s/%s", itemID, bandName)}
		fmt.Fprintf(os.Stderr, "  [resuming] %s (%s / %s, %s remaining)\n", filename, formatBytes(offset), formatBytes(total), formatBytes(total-offset))

		_, err = io.Copy(f, pr)
		if err != nil {
			return "", false, fmt.Errorf("write file: %w", err)
		}
		info, err := os.Stat(destPath)
		if err != nil {
			os.Remove(destPath)
			return "", false, fmt.Errorf("stat file: %w", err)
		}
		if info.Size() != total {
			os.Remove(destPath)
			return "", false, fmt.Errorf("size mismatch: got %s, expected %s", formatBytes(info.Size()), formatBytes(total))
		}
		return destPath, false, nil

	case http.StatusRequestedRangeNotSatisfiable:
		return destPath, true, nil

	default:
		return "", false, fmt.Errorf("HTTP %d for %s", resp.StatusCode, asset.Href)
	}
}

func downloadWorker(tasks <-chan downloadTask, results chan<- downloadResult) {
	for task := range tasks {
		var path string
		var skipped bool
		var err error
		for attempt := 0; attempt <= task.maxRetries; attempt++ {
			path, skipped, err = DownloadAsset(task.asset, task.destDir, task.itemID, task.band)
			if err == nil || skipped {
				break
			}
			if attempt < task.maxRetries {
				wait := time.Duration(attempt+1) * time.Second
				fmt.Fprintf(os.Stderr, "  [retry] %s/%s in %.0fs (attempt %d/%d): %v\n", task.itemID, task.band, wait.Seconds(), attempt+1, task.maxRetries, err)
				time.Sleep(wait)
			}
		}
		results <- downloadResult{path: path, skipped: skipped, err: err, task: task}
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

func findGDALTool(name string) string {
	exeName := name + ".exe"
	if _, err := os.Stat(exeName); err == nil {
		// 检查关键 DLL 是否也在当前目录，防止用户只拷贝了 exe 导致 0xc0000135
		if _, err := os.Stat("gdal305.dll"); err == nil {
			absPath, _ := filepath.Abs(exeName)
			return absPath
		}
	}
	return name
}

func gdalEnv() []string {
	env := os.Environ()
	if _, err := os.Stat("share/proj"); err == nil {
		projDir, _ := filepath.Abs("share/proj")
		env = append(env, "PROJ_DATA="+projDir)
	} else if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		projPath := filepath.Join(exeDir, "share", "proj")
		if _, err := os.Stat(projPath); err == nil {
			env = append(env, "PROJ_DATA="+projPath)
		}
	}
	return env
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

	byteName := fmt.Sprintf("%s_byte.tif", itemID)
	bytePath := filepath.Join(destDir, byteName)
	if _, err := os.Stat(bytePath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", byteName)
		return nil
	}

	vrtPath := filepath.Join(destDir, fmt.Sprintf("%s_rgb.vrt", itemID))
	buildCmd := exec.Command(findGDALTool("gdalbuildvrt"), append([]string{"-separate", vrtPath}, bandPaths...)...)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	buildCmd.Env = gdalEnv()
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("gdalbuildvrt failed: %w", err)
	}
	defer os.Remove(vrtPath)

	rgbPath := filepath.Join(destDir, fmt.Sprintf("%s_RGB.tif", itemID))
	transCmd := exec.Command(findGDALTool("gdal_translate"), vrtPath, rgbPath)
	transCmd.Stdout = os.Stdout
	transCmd.Stderr = os.Stderr
	transCmd.Env = gdalEnv()
	if err := transCmd.Run(); err != nil {
		return fmt.Errorf("gdal_translate failed: %w", err)
	}

	// 固定 0-3000 拉伸到 1-255，0 保留为 nodata
	args := []string{
		"-ot", "Byte",
		"-a_nodata", "0",
		"-scale_1", "0", "3000", "1", "255",
		"-scale_2", "0", "3000", "1", "255",
		"-scale_3", "0", "3000", "1", "255",
		rgbPath, bytePath,
	}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdal_translate"), strings.Join(args, " "))

	byteCmd := exec.Command(findGDALTool("gdal_translate"), args...)
	byteCmd.Stdout = os.Stdout
	byteCmd.Stderr = os.Stderr
	byteCmd.Env = gdalEnv()
	if err := byteCmd.Run(); err != nil {
		return fmt.Errorf("gdal_translate to byte failed: %w", err)
	}

	fmt.Printf("  [rgb] %s  %s\n", rgbPath, bytePath)
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
	fmt.Printf("  Workers: %d\n", cfg.MaxWorkers)
	fmt.Printf("  Retries: %d\n\n", cfg.MaxRetries)

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

	// 为已有数据补生成 KML
	existingItems := scanExistingItems(*destDir)
	if len(existingItems) > 0 {
		fmt.Println("\n=== Checking existing KML ===")
		for itemID := range existingItems {
			kmlPath := filepath.Join(*destDir, itemID+".kml")
			if _, err := os.Stat(kmlPath); err == nil {
				continue
			}
			fmt.Printf("  [kml fetch] %s\n", itemID)
			geom, err := fetchItemGeometry(itemID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  [kml fail] %s: %v\n", itemID, err)
				continue
			}
			item := STACItem{ID: itemID, Geometry: geom}
			if _, err := SaveKML(item, *destDir); err != nil {
				fmt.Fprintf(os.Stderr, "  [kml fail] %s: %v\n", itemID, err)
			}
		}
	}

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
		if _, err := SaveKML(item, *destDir); err != nil {
			fmt.Fprintf(os.Stderr, "  [kml skip] %s: %v\n", item.ID, err)
		}
		for _, band := range cfg.Bands {
			asset, ok := item.Assets[band]
			if !ok {
				fmt.Printf("  [warn] band '%s' not available\n", band)
				continue
			}
			tasks <- downloadTask{itemID: item.ID, band: band, asset: asset, destDir: *destDir, maxRetries: cfg.MaxRetries}
			total++
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

	fmt.Println("\n=== Building RGB ===")
	for _, item := range items {
		if err := BuildRGB(*destDir, item.ID); err != nil {
			fmt.Fprintf(os.Stderr, "  [rgb skip] %s: %v\n", item.ID, err)
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
