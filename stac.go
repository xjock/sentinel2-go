package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cdseBandMap translates user-friendly band names to CDSE STAC asset keys.
var cdseBandMap = map[string]string{
	"coastal": "B01_60m", "blue": "B02_10m", "green": "B03_10m", "red": "B04_10m",
	"rededge1": "B05_20m", "rededge2": "B06_20m", "rededge3": "B07_20m",
	"nir": "B08_10m", "nir08": "B8A_20m", "nir09": "B09_60m",
	"swir16": "B11_20m", "swir22": "B12_20m", "scl": "SCL_20m",
	"aot": "AOT_20m", "wvp": "WVP_10m", "tci": "TCI_10m",
}

func resolveAssetKey(band, stacURL string) string {
	if strings.Contains(stacURL, "stac.dataspace.copernicus.eu") {
		if key, ok := cdseBandMap[band]; ok {
			return key
		}
	}
	return band
}

type STACItemCollection struct {
	Type     string     `json:"type"`
	Features []STACItem `json:"features"`
}

type Geometry struct {
	Type        string        `json:"type"`
	Coordinates [][][]float64 `json:"coordinates"`
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

type AlternateLink struct {
	Href string `json:"href"`
}

type Asset struct {
	Href      string                   `json:"href"`
	Type      string                   `json:"type"`
	Title     string                   `json:"title"`
	Roles     []string                 `json:"roles"`
	Alternate map[string]AlternateLink `json:"alternate,omitempty"`
}

type downloadTask struct {
	itemID     string
	band       string
	asset      Asset
	destDir    string
	maxRetries int
	auth       Authenticator
}

type downloadResult struct {
	path    string
	err     error
	skipped bool
	task    downloadTask
}

func SearchItems(opts SearchOptions, auth Authenticator) (*STACItemCollection, error) {
	if opts.Limit == 0 {
		opts.Limit = 10
	}
	bboxStr := fmt.Sprintf("%f,%f,%f,%f", opts.Bbox[0], opts.Bbox[1], opts.Bbox[2], opts.Bbox[3])
	datetime := fmt.Sprintf("%sT00:00:00Z/%sT23:59:59Z", opts.StartDate, opts.EndDate)

	stacURL := opts.STACURL
	if stacURL == "" {
		stacURL = EarthSearchURL
	}
	collection := opts.Collection
	if collection == "" {
		collection = Collection
	}

	u, err := url.Parse(stacURL + "/search")
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	q := u.Query()
	q.Set("collections", collection)
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
	if err := auth.Apply(req); err != nil {
		return nil, fmt.Errorf("authenticate request: %w", err)
	}

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

	var result STACItemCollection
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	return &result, nil
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
			fmt.Fprintf(&coords, "%f,%f,0 ", p[0], p[1])
		}
	}

	var extData strings.Builder
	extData.WriteString("      <ExtendedData>\n")
	writeData := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&extData, "        <Data name=\"%s\"><value>%s</value></Data>\n", name, value)
		}
	}
	writeData("id", item.ID)
	writeData("collection", item.Collection)
	writeData("datetime", item.Properties.Datetime)
	writeData("created", item.Properties.Created)
	writeData("granule_id", item.Properties.GranuleID)
	if item.Properties.CloudCover > 0 {
		fmt.Fprintf(&extData, "        <Data name=\"cloud_cover\"><value>%.2f</value></Data>\n", item.Properties.CloudCover)
	}
	if len(item.BBox) == 4 {
		fmt.Fprintf(&extData, "        <Data name=\"bbox\"><value>%.6f,%.6f,%.6f,%.6f</value></Data>\n", item.BBox[0], item.BBox[1], item.BBox[2], item.BBox[3])
	}
	extData.WriteString("      </ExtendedData>")

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
%s
      <Polygon>
        <outerBoundaryIs>
          <LinearRing>
            <coordinates>%s</coordinates>
          </LinearRing>
        </outerBoundaryIs>
      </Polygon>
    </Placemark>
  </Document>
</kml>`, item.ID, extData.String(), strings.TrimSpace(coords.String()))

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

func resolveDownloadURL(asset Asset) string {
	if strings.HasPrefix(asset.Href, "s3://") {
		if alt, ok := asset.Alternate["https"]; ok && alt.Href != "" {
			return alt.Href
		}
	}
	return asset.Href
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

func fetchItem(itemID, stacURL, collection string, auth Authenticator) (STACItem, error) {
	if stacURL == "" {
		stacURL = EarthSearchURL
	}
	if collection == "" {
		collection = Collection
	}
	u, err := url.Parse(fmt.Sprintf("%s/collections/%s/items/%s", stacURL, collection, itemID))
	if err != nil {
		return STACItem{}, fmt.Errorf("parse URL: %w", err)
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return STACItem{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/geo+json")
	if err := auth.Apply(req); err != nil {
		return STACItem{}, fmt.Errorf("authenticate request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return STACItem{}, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return STACItem{}, fmt.Errorf("STAC API returned %d: %s", resp.StatusCode, string(body))
	}

	var item STACItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return STACItem{}, fmt.Errorf("decode JSON: %w", err)
	}
	if item.Geometry.Type != "Polygon" || len(item.Geometry.Coordinates) == 0 {
		return STACItem{}, fmt.Errorf("no polygon geometry in response")
	}
	return item, nil
}

func assetExists(destDir, itemID, bandName string) bool {
	filename := fmt.Sprintf("%s_%s.tif", itemID, bandName)
	_, err := os.Stat(filepath.Join(destDir, filename))
	return err == nil
}

func DownloadAsset(asset Asset, destDir string, itemID string, bandName string, auth Authenticator) (string, bool, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", false, fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	filename := fmt.Sprintf("%s_%s.tif", itemID, bandName)
	destPath := filepath.Join(destDir, filename)

	var offset int64
	if info, err := os.Stat(destPath); err == nil {
		offset = info.Size()
	}

	url := resolveDownloadURL(asset)
	client := &http.Client{Timeout: DownloadTimeout}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", false, fmt.Errorf("create request: %w", err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	if err := auth.Apply(req); err != nil {
		return "", false, fmt.Errorf("authenticate request: %w", err)
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
		return "", false, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
}

func downloadWorker(tasks <-chan downloadTask, results chan<- downloadResult) {
	for task := range tasks {
		var path string
		var skipped bool
		var err error
		for attempt := 0; attempt <= task.maxRetries; attempt++ {
			path, skipped, err = DownloadAsset(task.asset, task.destDir, task.itemID, task.band, task.auth)
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
