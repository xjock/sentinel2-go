package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	byteName := fmt.Sprintf("%s_byte.tif", itemID)
	bytePath := filepath.Join(destDir, byteName)
	renewName := fmt.Sprintf("%s_byte_renew.tif", itemID)
	renewPath := filepath.Join(destDir, renewName)

	if _, err := os.Stat(renewPath); err == nil {
		fmt.Printf("  [skip] %s already exists\n", renewName)
		return nil
	}

	// 若已存在未renew的 _byte.tif，直接复用，不重新合成
	if _, err := os.Stat(bytePath); err == nil {
		fmt.Printf("  [reuse] %s, retrying renew\n", byteName)
		if err := renewByteTIFF(bytePath, renewPath, destDir); err != nil {
			fmt.Fprintf(os.Stderr, "  [renew skip] %s: %v\n", itemID, err)
			return nil
		}
		os.Remove(bytePath)
		fmt.Printf("  [renew] %s\n", renewName)
		return nil
	}

	bands := []string{"red", "green", "blue"}
	bandPaths := []string{}
	for _, band := range bands {
		p := filepath.Join(destDir, fmt.Sprintf("%s_%s.tif", itemID, band))
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("missing band %s: %w", band, err)
		}
		bandPaths = append(bandPaths, p)
	}

	vrtPath := filepath.Join(destDir, fmt.Sprintf("%s_rgb.vrt", itemID))
	buildCmd := exec.Command(findGDALTool("gdalbuildvrt"), append([]string{"-separate", vrtPath}, bandPaths...)...)
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdalbuildvrt"), strings.Join(append([]string{"-separate", vrtPath}, bandPaths...), " "))
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	buildCmd.Env = gdalEnv()
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("gdalbuildvrt failed: %w", err)
	}
	defer os.Remove(vrtPath)

	rgbPath := filepath.Join(destDir, fmt.Sprintf("%s_RGB.tif", itemID))
	transArgs := []string{vrtPath, rgbPath}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdal_translate"), strings.Join(transArgs, " "))
	transCmd := exec.Command(findGDALTool("gdal_translate"), transArgs...)
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
		"-scale_1", "0", "3000", "0", "255",
		"-scale_2", "0", "3000", "0", "255",
		"-scale_3", "0", "3000", "0", "255",
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

	if err := renewByteTIFF(bytePath, renewPath, destDir); err != nil {
		fmt.Fprintf(os.Stderr, "  [renew skip] %s: %v\n", itemID, err)
		return nil
	}

	// os.Remove(bytePath)
	// os.Remove(rgbPath)
	fmt.Printf("  [renew] %s\n", renewName)
	return nil
}

// buildRGBByte 接受显式 R/G/B 波段路径，直接生成拉伸后的 byte 合成图。
// 中间 VRT 写入 workDir，不产生独立的 _RGB.tif。
func buildRGBByte(redPath, greenPath, bluePath, bytePath, workDir string) error {
	vrtPath := filepath.Join(workDir, "rgb.vrt")
	buildArgs := []string{"-separate", vrtPath, redPath, greenPath, bluePath}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdalbuildvrt"), strings.Join(buildArgs, " "))
	buildCmd := exec.Command(findGDALTool("gdalbuildvrt"), buildArgs...)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	buildCmd.Env = gdalEnv()
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("gdalbuildvrt failed: %w", err)
	}

	args := []string{
		"-ot", "Byte",
		"-a_nodata", "0",
		"-scale_1", "0", "3000", "0", "255",
		"-scale_2", "0", "3000", "0", "255",
		"-scale_3", "0", "3000", "0", "255",
		vrtPath, bytePath,
	}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdal_translate"), strings.Join(args, " "))

	byteCmd := exec.Command(findGDALTool("gdal_translate"), args...)
	byteCmd.Stdout = os.Stdout
	byteCmd.Stderr = os.Stderr
	byteCmd.Env = gdalEnv()
	if err := byteCmd.Run(); err != nil {
		return fmt.Errorf("gdal_translate to byte failed: %w", err)
	}
	return nil
}

// renewByteTIFF 对 bytePath 跑 gdal_trace_outline → gdalwarp -cutline → pkRenew，
// 把修复后的图写到 outputPath。中间 shapefile 与 masked tif 落在 workDir 并清理。
// 原 bytePath 不在此函数中删除，由调用者负责。失败时不会留下半成品 outputPath。
func renewByteTIFF(bytePath, outputPath, workDir string) error {
	base := strings.TrimSuffix(filepath.Base(bytePath), filepath.Ext(bytePath))
	outlineBase := filepath.Join(workDir, base+"_outline")
	outlinePath := outlineBase + ".shp"
	maskedPath := filepath.Join(workDir, base+"_masked.tif")

	traceArgs := []string{bytePath, "-ndv", "0", "-out-cs", "en", "-ogr-out", outlinePath}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdal_trace_outline"), strings.Join(traceArgs, " "))
	traceCmd := exec.Command(findGDALTool("gdal_trace_outline"), traceArgs...)
	traceCmd.Stdout = os.Stdout
	traceCmd.Stderr = os.Stderr
	traceCmd.Env = gdalEnv()
	if err := traceCmd.Run(); err != nil {
		return fmt.Errorf("gdal_trace_outline failed: %w", err)
	}

	warpArgs := []string{"-overwrite", "-cutline", outlinePath, "-dstnodata", "0", bytePath, maskedPath}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("gdalwarp"), strings.Join(warpArgs, " "))
	warpCmd := exec.Command(findGDALTool("gdalwarp"), warpArgs...)
	warpCmd.Stdout = os.Stdout
	warpCmd.Stderr = os.Stderr
	warpCmd.Env = gdalEnv()
	if err := warpCmd.Run(); err != nil {
		return fmt.Errorf("gdalwarp failed: %w", err)
	}

	renewArgs := []string{"-recover-nodata", maskedPath, outputPath}
	fmt.Printf("  [cmd] %s %s\n", findGDALTool("pkRenew"), strings.Join(renewArgs, " "))
	renewCmd := exec.Command(findGDALTool("pkRenew"), renewArgs...)
	renewCmd.Stdout = os.Stdout
	renewCmd.Stderr = os.Stderr
	renewCmd.Env = gdalEnv()
	if err := renewCmd.Run(); err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("pkRenew failed: %w", err)
	}
	return nil
}
