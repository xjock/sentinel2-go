package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

type Config struct {
	BBox       []float64   `json:"bbox"`
	StartDate  string      `json:"start_date"`
	EndDate    string      `json:"end_date"`
	MaxCloud   float64     `json:"max_cloud"`
	Bands      []string    `json:"bands"`
	Limit      int         `json:"limit"`
	MaxWorkers int         `json:"max_workers"`
	MaxRetries int         `json:"max_retries"`
	STACURL    string      `json:"stac_url,omitempty"`
	Collection string      `json:"collection,omitempty"`
	Auth       *AuthConfig `json:"auth,omitempty"`
}

type SearchOptions struct {
	Bbox       []float64
	StartDate  string
	EndDate    string
	Limit      int
	MaxCloud   float64
	STACURL    string
	Collection string
}

type AuthConfig struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// Authenticator attaches credentials to an HTTP request.
type Authenticator interface {
	Apply(req *http.Request) error
}

// NoOpAuth does nothing; used for open STAC APIs such as Earth Search.
type NoOpAuth struct{}

func (NoOpAuth) Apply(req *http.Request) error { return nil }

// CDSEAuth implements CDSE Keycloak OAuth2 password grant flow.
type CDSEAuth struct {
	Username string
	Password string

	mu        sync.RWMutex
	token     string
	expiresAt time.Time
	margin    time.Duration
}

func NewCDSEAuth(username, password string) *CDSEAuth {
	return &CDSEAuth{
		Username: username,
		Password: password,
		margin:   30 * time.Second,
	}
}

func (o *CDSEAuth) Apply(req *http.Request) error {
	tok, err := o.tokenWithRefresh(req.Context())
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (o *CDSEAuth) tokenWithRefresh(ctx context.Context) (string, error) {
	o.mu.RLock()
	tok, valid := o.token, time.Now().Add(o.margin).Before(o.expiresAt)
	o.mu.RUnlock()
	if valid && tok != "" {
		return tok, nil
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if time.Now().Add(o.margin).Before(o.expiresAt) && o.token != "" {
		return o.token, nil
	}
	return o.fetchToken(ctx)
}

func (o *CDSEAuth) fetchToken(ctx context.Context) (string, error) {
	data := url.Values{}
	data.Set("grant_type", "password")
	data.Set("client_id", "cdse-public")
	data.Set("username", o.Username)
	data.Set("password", o.Password)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://identity.dataspace.copernicus.eu/auth/realms/CDSE/protocol/openid-connect/token", strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	o.token = tr.AccessToken
	o.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return o.token, nil
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

func resolveEnv(s string) string {
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		return os.Getenv(s[2 : len(s)-1])
	}
	return s
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
	if cfg.STACURL == "" {
		cfg.STACURL = EarthSearchURL
	}
	if cfg.Collection == "" {
		cfg.Collection = Collection
	}
	return &cfg, nil
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

var stdinReader = bufio.NewReader(os.Stdin)

func readLine(prompt string) string {
	fmt.Print(prompt)
	line, _ := stdinReader.ReadString('\n')
	return strings.TrimSpace(line)
}

func setupAuthWizard() {
	fmt.Println("=== sentinel2-go 认证配置 ===")
	fmt.Println()
	fmt.Println("选择数据源:")
	fmt.Println("  1) Earth Search — 无需认证")
	fmt.Println("  2) Copernicus Data Space Ecosystem (CDSE)")
	fmt.Println("  3) 自定义 STAC API")
	fmt.Println()

	choice := readLine("选择 [1-3]: ")

	switch choice {
	case "1":
		if err := os.Remove(settingsPath()); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "删除配置失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("\n已清除配置。")
		fmt.Println("Earth Search 为默认数据源，无需认证。")

	case "2":
		fmt.Println("\n--- CDSE 配置 ---")
		fmt.Println("使用 CDSE 账号的用户名和密码进行认证。")
		fmt.Println("访问 https://dataspace.copernicus.eu/ 注册账号。")
		fmt.Println()
		username := readLine("邮箱（用户名）: ")
		password := readLine("密码: ")
		if username == "" || password == "" {
			fmt.Println("\n错误: 用户名和密码不能为空。")
			os.Exit(1)
		}
		settings := &Settings{
			Source:     "cdse",
			STACURL:    "https://stac.dataspace.copernicus.eu/v1",
			Collection: "sentinel-2-l2a",
			Auth:       &AuthConfig{Username: username, Password: password},
		}
		if err := saveSettings(settings); err != nil {
			fmt.Fprintf(os.Stderr, "保存配置失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n配置已保存到: %s\n", settingsPath())
		fmt.Println("文件权限: 0600（仅所有者可读写）")

	case "3":
		fmt.Println("\n--- 自定义 STAC API 配置 ---")
		stacURL := readLine("STAC API 地址: ")
		collection := readLine("Collection 名称: ")
		if stacURL == "" || collection == "" {
			fmt.Println("\n错误: 地址和名称不能为空。")
			os.Exit(1)
		}
		settings := &Settings{
			Source:     "custom",
			STACURL:    stacURL,
			Collection: collection,
		}
		if err := saveSettings(settings); err != nil {
			fmt.Fprintf(os.Stderr, "保存配置失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n配置已保存到: %s\n", settingsPath())
		fmt.Println("文件权限: 0600（仅所有者可读写）")

	default:
		fmt.Println("\n无效选择，请重新运行并选择 1、2 或 3。")
		os.Exit(1)
	}
}

// ---------- Settings & Web-based Setup ----------

func settingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".sentinel2-go", "settings.json")
}

type Settings struct {
	Source     string      `json:"source"`
	STACURL    string      `json:"stac_url,omitempty"`
	Collection string      `json:"collection,omitempty"`
	Auth       *AuthConfig `json:"auth,omitempty"`
}

func loadSettings() (*Settings, error) {
	path := settingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveSettings(s *Settings) error {
	path := settingsPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func needsSetup() bool {
	_, err := os.Stat(settingsPath())
	return os.IsNotExist(err)
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}

const setupHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>sentinel2-go Setup</title>
<style>
  * { box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    background: #f5f7fa;
    margin: 0;
    padding: 40px 20px;
    display: flex;
    justify-content: center;
  }
  .container {
    background: #fff;
    border-radius: 12px;
    box-shadow: 0 2px 12px rgba(0,0,0,0.08);
    max-width: 480px;
    width: 100%;
    padding: 32px;
  }
  h1 { margin: 0 0 8px; font-size: 22px; color: #1a1a2e; }
  p.desc { margin: 0 0 24px; color: #666; font-size: 14px; }
  .field { margin-bottom: 16px; }
  label {
    display: block;
    font-size: 13px;
    font-weight: 600;
    color: #444;
    margin-bottom: 6px;
  }
  input[type="text"], input[type="password"], select {
    width: 100%;
    padding: 10px 12px;
    border: 1px solid #d1d5db;
    border-radius: 8px;
    font-size: 14px;
    background: #fafbfc;
  }
  input:focus, select:focus {
    outline: none;
    border-color: #3b82f6;
    background: #fff;
  }
  .hint {
    font-size: 12px;
    color: #888;
    margin-top: 4px;
  }
  .hidden { display: none; }
  .panel {
    border-left: 4px solid #3b82f6;
    padding: 16px;
    background: #f8fafc;
    border-radius: 0 8px 8px 0;
  }
  .panel h3 { margin: 0 0 12px; font-size: 15px; color: #1e3a5f; }
  .steps { margin-bottom: 16px; }
  .steps p { margin: 0 0 8px; font-size: 13px; color: #555; line-height: 1.5; }
  .steps a { color: #3b82f6; }
  button {
    width: 100%;
    padding: 12px;
    background: #3b82f6;
    color: #fff;
    border: none;
    border-radius: 8px;
    font-size: 15px;
    font-weight: 600;
    cursor: pointer;
    margin-top: 8px;
  }
  button:hover { background: #2563eb; }
  .earth { border-left: 4px solid #10b981; padding-left: 12px; background: #f0fdf4; border-radius: 0 8px 8px 0; }
  .cdse { border-left: 4px solid #3b82f6; padding-left: 12px; background: #eff6ff; border-radius: 0 8px 8px 0; }
  .custom { border-left: 4px solid #f59e0b; padding-left: 12px; background: #fffbeb; border-radius: 0 8px 8px 0; }
</style>
</head>
<body>
<div class="container">
  <h1>sentinel2-go 数据源配置</h1>
  <p class="desc">选择数据源和认证方式。</p>
  <form method="POST" action="/">
    <div class="field">
      <label for="source">数据源</label>
      <select id="source" name="source" onchange="onSourceChange()">
        <option value="earth_search">Earth Search STAC API（无需认证）</option>
        <option value="cdse">Copernicus Data Space Ecosystem (CDSE)</option>
        <option value="custom">自定义 STAC API</option>
      </select>
    </div>

    <div id="cdse-box" class="hidden">
      <div class="panel cdse">
        <h3>CDSE 账号设置</h3>
        <div class="steps">
          <p><strong>第 1 步：</strong>前往 <a href="https://dataspace.copernicus.eu/" target="_blank">dataspace.copernicus.eu</a> 注册账号，点击右上角用户图标 → REGISTER，填写信息后查收验证邮件完成验证。</p>
          <p><strong>第 2 步：</strong>在下方填写 CDSE 登录邮箱和密码。</p>
        </div>

        <div class="field">
          <label>邮箱（用户名）</label>
          <input type="text" name="cdse_username" placeholder="your@email.com">
        </div>
        <div class="field">
          <label>密码</label>
          <input type="password" name="cdse_password" placeholder="CDSE 登录密码">
        </div>
        <p class="hint">使用 CDSE 登录邮箱和密码，密码保存在本地。</p>
      </div>
    </div>

    <div id="custom-box" class="field custom hidden">
      <div class="field">
        <label>STAC API 地址</label>
        <input type="text" name="stac_url" placeholder="https://example.com/stac">
      </div>
      <div class="field">
        <label>Collection 名称</label>
        <input type="text" name="collection" placeholder="SENTINEL-2">
      </div>
    </div>

    <button type="submit">保存并继续</button>
  </form>
</div>
<script>
function onSourceChange() {
  const v = document.getElementById('source').value;
  document.getElementById('cdse-box').classList.toggle('hidden', v !== 'cdse');
  document.getElementById('custom-box').classList.toggle('hidden', v !== 'custom');
}
onSourceChange();
</script>
</body>
</html>`

const successHTML = `<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><title>配置完成</title>
<style>
  body { font-family: sans-serif; background: #f5f7fa; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; }
  .box { background: #fff; padding: 40px; border-radius: 12px; text-align: center; box-shadow: 0 2px 12px rgba(0,0,0,0.08); }
  h1 { color: #10b981; margin: 0 0 12px; }
  p { color: #666; margin: 0; }
</style>
</head>
<body>
<div class="box">
  <h1>配置完成</h1>
  <p>可以关闭此页面，程序将继续运行。</p>
</div>
</body>
</html>`

func runSetupWizard() (*Settings, error) {
	done := make(chan *Settings, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Bad request", http.StatusBadRequest)
				return
			}

			source := r.FormValue("source")
			settings := &Settings{Source: source}

			switch source {
			case "earth_search":
				settings.STACURL = EarthSearchURL
				settings.Collection = Collection
			case "cdse":
				settings.STACURL = "https://stac.dataspace.copernicus.eu/v1"
				settings.Collection = "sentinel-2-l2a"
				settings.Auth = &AuthConfig{
					Username: strings.TrimSpace(r.FormValue("cdse_username")),
					Password: strings.TrimSpace(r.FormValue("cdse_password")),
				}
			case "custom":
				settings.STACURL = strings.TrimSpace(r.FormValue("stac_url"))
				settings.Collection = strings.TrimSpace(r.FormValue("collection"))
			default:
				http.Error(w, "Invalid source", http.StatusBadRequest)
				return
			}

			if err := saveSettings(settings); err != nil {
				http.Error(w, "Failed to save settings", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, successHTML)
			done <- settings
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, setupHTML)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	fmt.Printf("正在浏览器中打开配置页面：%s\n", url)
	if err := openBrowser(url); err != nil {
		fmt.Printf("请手动在浏览器中打开以下地址：%s\n", url)
	}

	select {
	case settings := <-done:
		return settings, nil
	case <-time.After(10 * time.Minute):
		return nil, fmt.Errorf("setup timed out after 10 minutes")
	}
}

func mergeSettings(cfg *Config) {
	s, err := loadSettings()
	if err != nil || s == nil {
		return
	}
	if cfg.STACURL == "" || cfg.STACURL == EarthSearchURL {
		if s.STACURL != "" {
			cfg.STACURL = s.STACURL
		}
	}
	if cfg.Collection == "" || cfg.Collection == Collection {
		if s.Collection != "" {
			cfg.Collection = s.Collection
		}
	}
	if cfg.Auth == nil || cfg.Auth.Username == "" {
		if s.Auth != nil && s.Auth.Username != "" {
			cfg.Auth = s.Auth
		}
	}
}

func main() {
	configPath := flag.String("config", "config.json", "Path to configuration JSON file")
	destDir := flag.String("dest", "./sentinel2_data", "Destination directory for downloaded files")
	setupAuth := flag.Bool("setup-auth", false, "Interactive authentication setup wizard (CLI)")
	setupFlag := flag.Bool("setup", false, "Open web-based setup wizard")
	flag.Parse()

	if *setupAuth {
		setupAuthWizard()
		return
	}

	if *setupFlag || needsSetup() {
		_, err := runSetupWizard()
		if err != nil {
			fmt.Fprintf(os.Stderr, "配置失败：%v\n", err)
			os.Exit(1)
		}
		fmt.Println("配置已保存。")
		if *setupFlag {
			return
		}
		// First-run: continue with the saved settings
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	mergeSettings(cfg)

	var auth Authenticator = NoOpAuth{}
	if cfg.Auth != nil && cfg.Auth.Username != "" && cfg.Auth.Password != "" {
		auth = NewCDSEAuth(cfg.Auth.Username, cfg.Auth.Password)
	}

	opts := SearchOptions{
		Bbox:       cfg.BBox,
		StartDate:  cfg.StartDate,
		EndDate:    cfg.EndDate,
		Limit:      cfg.Limit,
		MaxCloud:   cfg.MaxCloud,
		STACURL:    cfg.STACURL,
		Collection: cfg.Collection,
	}

	fmt.Printf("Searching Sentinel-2 L2A data...\n")
	fmt.Printf("  Config:    %s\n", *configPath)
	fmt.Printf("  Dest:      %s\n", *destDir)
	fmt.Printf("  STAC URL:  %s\n", opts.STACURL)
	fmt.Printf("  Collection: %s\n", opts.Collection)
	fmt.Printf("  Auth:      %s\n", func() string {
		if cfg.Auth != nil {
			return "OAuth2 (CDSE)"
		}
		return "none"
	}())
	fmt.Printf("  BBox:      %v (west, south, east, north)\n", opts.Bbox)
	fmt.Printf("  Date:      %s to %s\n", opts.StartDate, opts.EndDate)
	fmt.Printf("  Cloud:     <= %.0f%%\n", opts.MaxCloud)
	fmt.Printf("  Bands:     %v\n", cfg.Bands)
	fmt.Printf("  Workers:   %d\n", cfg.MaxWorkers)
	fmt.Printf("  Retries:   %d\n\n", cfg.MaxRetries)

	stacCollection, err := SearchItems(opts, auth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Search failed: %v\n", err)
		os.Exit(1)
	}

	if len(stacCollection.Features) == 0 {
		fmt.Println("No items found.")
		return
	}

	items := FilterItemsByCloud(stacCollection.Features, opts.MaxCloud)
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
			item, err := fetchItem(itemID, cfg.STACURL, cfg.Collection, auth)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  [kml fail] %s: %v\n", itemID, err)
				continue
			}
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
			assetKey := resolveAssetKey(band, cfg.STACURL)
			asset, ok := item.Assets[assetKey]
			if !ok {
				fmt.Printf("  [warn] band '%s' not available (tried '%s')\n", band, assetKey)
				continue
			}
			tasks <- downloadTask{itemID: item.ID, band: band, asset: asset, destDir: *destDir, maxRetries: cfg.MaxRetries, auth: auth}
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
