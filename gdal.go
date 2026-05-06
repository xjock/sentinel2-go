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

// buildRGBByte 接受显式 R/G/B 波段路径，直接生成拉伸后的 byte 合成图。
// 中间 VRT 写入 workDir，不产生独立的 _RGB.tif。
func buildRGBByte(redPath, greenPath, bluePath, bytePath, workDir string) error {
	vrtPath := filepath.Join(workDir, "rgb.vrt")
	buildCmd := exec.Command(findGDALTool("gdalbuildvrt"),
		"-separate", vrtPath, redPath, greenPath, bluePath)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	buildCmd.Env = gdalEnv()
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("gdalbuildvrt failed: %w", err)
	}

	args := []string{
		"-ot", "Byte",
		"-a_nodata", "0",
		"-scale_1", "0", "3000", "1", "255",
		"-scale_2", "0", "3000", "1", "255",
		"-scale_3", "0", "3000", "1", "255",
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
