package main

import (
	"context"
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

const (
	cdseODataCatalogURL  = "https://catalogue.dataspace.copernicus.eu/odata/v1/Products"
	cdseODataDownloadURL = "https://zipper.dataspace.copernicus.eu/odata/v1/Products"
)

// OData types for CDSE OData Catalog API.
type odataProduct struct {
	ID            string    `json:"Id"`
	Name          string    `json:"Name"`
	ContentLength int64     `json:"ContentLength"`
	OriginDate    time.Time `json:"OriginDate"`
	Online        bool      `json:"Online"`
	GeoFootprint  Geometry  `json:"GeoFootprint"`
}

type odataCatalogResponse struct {
	Value []odataProduct `json:"value"`
	Count int            `json:"@odata.count"`
}

func SaveKMLForOData(product odataProduct, destDir string) (string, error) {
	if product.GeoFootprint.Type != "Polygon" || len(product.GeoFootprint.Coordinates) == 0 {
		return "", fmt.Errorf("no polygon geometry for %s", product.Name)
	}

	kmlPath := filepath.Join(destDir, product.Name+".kml")
	if _, err := os.Stat(kmlPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", product.Name+".kml")
		return kmlPath, nil
	}

	ring := product.GeoFootprint.Coordinates[0]
	var coords strings.Builder
	for _, p := range ring {
		if len(p) >= 2 {
			fmt.Fprintf(&coords, "%f,%f,0 ", p[0], p[1])
		}
	}

	var extData strings.Builder
	extData.WriteString("      <ExtendedData>\n")
	fmt.Fprintf(&extData, "        <Data name=\"id\"><value>%s</value></Data>\n", product.Name)
	if !product.OriginDate.IsZero() {
		fmt.Fprintf(&extData, "        <Data name=\"datetime\"><value>%s</value></Data>\n", product.OriginDate.Format(time.RFC3339))
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
</kml>`, product.Name, extData.String(), strings.TrimSpace(coords.String()))

	if err := os.WriteFile(kmlPath, []byte(kml), 0644); err != nil {
		return "", fmt.Errorf("write kml: %w", err)
	}
	fmt.Printf("  [saved] %s\n", product.Name+".kml")
	return kmlPath, nil
}

// ---------- CDSE OData Flow ----------

func queryODataProducts(auth Authenticator, cfg *Config) ([]odataProduct, error) {
	if len(cfg.BBox) != 4 {
		return nil, fmt.Errorf("bbox must have 4 elements [west,south,east,north]")
	}
	west, south, east, north := cfg.BBox[0], cfg.BBox[1], cfg.BBox[2], cfg.BBox[3]

	polygon := fmt.Sprintf(
		"POLYGON((%f %f,%f %f,%f %f,%f %f,%f %f))",
		west, south, east, south, east, north, west, north, west, south,
	)

	filters := []string{
		"Collection/Name eq 'SENTINEL-2'",
		"Attributes/OData.CSC.StringAttribute/any(att:att/Name eq 'productType' and att/OData.CSC.StringAttribute/Value eq 'S2MSI2A')",
		fmt.Sprintf("ContentDate/Start gt %sT00:00:00.000Z", cfg.StartDate),
		fmt.Sprintf("ContentDate/Start lt %sT23:59:59.000Z", cfg.EndDate),
	}
	if cfg.MaxCloud > 0 {
		filters = append(filters, fmt.Sprintf(
			"Attributes/OData.CSC.DoubleAttribute/any(att:att/Name eq 'cloudCover' and att/OData.CSC.DoubleAttribute/Value lt %.1f)",
			cfg.MaxCloud,
		))
	}
	filters = append(filters, fmt.Sprintf(
		"OData.CSC.Intersects(area=geography'SRID=4326;%s')", polygon,
	))

	q := url.Values{}
	q.Set("$filter", strings.Join(filters, " and "))
	q.Set("$orderby", "ContentDate/Start desc")
	q.Set("$top", fmt.Sprintf("%d", cfg.Limit))
	q.Set("$count", "true")
	q.Set("$select", "Id,Name,ContentLength,OriginDate,Online,GeoFootprint")

	req, err := http.NewRequestWithContext(context.Background(), "GET", cdseODataCatalogURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if err := auth.Apply(req); err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog returned %d: %s", resp.StatusCode, string(body))
	}

	var result odataCatalogResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Value, nil
}

func downloadODataProductOnce(auth Authenticator, product odataProduct, destDir string, offset int64) (int64, error) {
	downloadURL := fmt.Sprintf("%s(%s)/$value", cdseODataDownloadURL, product.ID)

	req, err := http.NewRequestWithContext(context.Background(), "GET", downloadURL, nil)
	if err != nil {
		return offset, fmt.Errorf("create request: %w", err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	if err := auth.Apply(req); err != nil {
		return offset, fmt.Errorf("authenticate: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return offset, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		if offset > 0 {
			if resp.ContentLength > 0 && offset == resp.ContentLength {
				return offset, nil
			}
			// Server doesn't support Range; restart from scratch.
			offset = 0
		}
		return odataWriteBody(resp, product, destDir, offset, product.ContentLength)

	case http.StatusPartialContent:
		total := parseContentRangeTotal(resp.Header.Get("Content-Range"))
		if total == 0 && resp.ContentLength > 0 {
			total = offset + resp.ContentLength
		}
		return odataWriteBody(resp, product, destDir, offset, total)

	case http.StatusRequestedRangeNotSatisfiable:
		return offset, nil

	default:
		body, _ := io.ReadAll(resp.Body)
		return offset, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
}

func odataWriteBody(resp *http.Response, product odataProduct, destDir string, offset, total int64) (int64, error) {
	tmpPath := filepath.Join(destDir, product.Name+".zip.tmp")

	var f *os.File
	var err error
	if offset > 0 {
		f, err = os.OpenFile(tmpPath, os.O_APPEND|os.O_WRONLY, 0644)
	} else {
		f, err = os.Create(tmpPath)
	}
	if err != nil {
		return offset, fmt.Errorf("open file: %w", err)
	}

	pr := &progressReader{r: resp.Body, total: total, current: offset, label: product.Name}
	_, err = io.Copy(f, pr)
	f.Close()
	if err != nil {
		return offset, fmt.Errorf("write file: %w", err)
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		return offset, fmt.Errorf("stat file: %w", err)
	}
	return info.Size(), nil
}

func downloadODataProduct(auth Authenticator, product odataProduct, destDir string, maxRetries int) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	outputPath := filepath.Join(destDir, product.Name+".zip")
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", product.Name+".zip")
		return nil
	}

	tmpPath := outputPath + ".tmp"
	var offset int64
	if info, err := os.Stat(tmpPath); err == nil {
		offset = info.Size()
		pct := ""
		if product.ContentLength > 0 {
			pct = fmt.Sprintf(" (%d%%)", int(offset*100/product.ContentLength))
		}
		fmt.Printf("  [resuming] %s from %s%s\n", product.Name, formatBytes(offset), pct)
	} else {
		fmt.Printf("  [downloading] %s (%.1f MB)\n", product.Name, float64(product.ContentLength)/1024/1024)
	}

	var finalSize int64
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		finalSize, err = downloadODataProductOnce(auth, product, destDir, offset)
		if err == nil {
			break
		}
		if attempt < maxRetries {
			wait := time.Duration(attempt+1) * time.Second
			fmt.Fprintf(os.Stderr, "  [retry] %s in %.0fs (attempt %d/%d): %v\n", product.Name, wait.Seconds(), attempt+1, maxRetries, err)
			time.Sleep(wait)
		}
	}
	if err != nil {
		return err
	}

	if product.ContentLength > 0 && finalSize != product.ContentLength {
		os.Remove(tmpPath)
		return fmt.Errorf("size mismatch: got %s, expected %s", formatBytes(finalSize), formatBytes(product.ContentLength))
	}

	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("rename file: %w", err)
	}

	fmt.Printf("  [saved] %s (%s)\n", outputPath, formatBytes(finalSize))
	return nil
}

func runODataFlow(cfg *Config, auth Authenticator, destDir string) {
	fmt.Println("\n=== CDSE OData Search ===")
	products, err := queryODataProducts(auth, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "OData search failed: %v\n", err)
		os.Exit(1)
	}

	if len(products) == 0 {
		fmt.Println("No products found.")
		return
	}

	fmt.Printf("\nFound %d products\n\n", len(products))
	for i, p := range products {
		sizeMB := float64(p.ContentLength) / 1024 / 1024
		online := "online"
		if !p.Online {
			online = "OFFLINE (LTA)"
		}
		fmt.Printf("[%d] %s | %s | %.1f MB | %s\n",
			i+1, p.Name, p.OriginDate.Format("2006-01-02"), sizeMB, online)
	}

	fmt.Println("\n=== Saving KML ===")
	for _, p := range products {
		if _, err := SaveKMLForOData(p, destDir); err != nil {
			fmt.Fprintf(os.Stderr, "  [kml skip] %s: %v\n", p.Name, err)
		}
	}

	fmt.Println("\n=== Downloading Products ===")
	failed := 0
	skipped := 0
	for _, p := range products {
		if !p.Online {
			fmt.Printf("  [skip] %s is offline in LTA\n", p.Name)
			skipped++
			continue
		}
		if err := downloadODataProduct(auth, p, destDir, cfg.MaxRetries); err != nil {
			fmt.Fprintf(os.Stderr, "  [error] %s: %v\n", p.Name, err)
			failed++
		}
	}

	fmt.Println("\nDone.")
	if failed > 0 {
		fmt.Printf("%d downloads failed.\n", failed)
		os.Exit(1)
	}
	if skipped > 0 {
		fmt.Printf("%d products were offline (LTA), skipped.\n", skipped)
	}
}
