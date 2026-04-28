# Sentinel-2 Go 下载器

一个轻量级的 Go 程序，用于从 [Earth Search](https://earth-search.aws.element84.com/) STAC API 查询并下载公开的 Sentinel-2 L2A 卫星影像，**无需任何认证**。

## 特性

- 无需 API Key 或凭证
- 支持按边界框、日期范围、云量过滤查询
- 下载单波段 Cloud Optimized GeoTIFF (COG) 文件
- 自动跳过已下载文件
- 纯 Go 实现，零外部依赖

## 环境要求

- [Go](https://go.dev/) 1.21 或更高版本

## 快速开始

```bash
git clone <你的仓库地址>
cd sentinel2-go
go run main.go
```

程序会：
1. 根据配置的边界框和日期范围搜索 Sentinel-2 L2A 数据
2. 按云量过滤结果
3. 将请求的波段下载到 `./sentinel2_data/` 目录

## 配置

编辑 `main.go`，修改 `main()` 函数开头的变量：

```go
bbox := []float64{116.2, 39.8, 116.6, 40.0}   // 西, 南, 东, 北
startDate := "2025-01-01"
endDate   := "2025-01-15"
maxCloud  := 20.0                               // 最大云量 %
bandsToDownload := []string{"red", "green", "blue", "nir"}
destDir := "./sentinel2_data"
```

### 边界框获取方式

- [geojson.io](http://geojson.io/) 画一个矩形，右下角显示坐标
- Python: `from shapely import box; list(box(minx, miny, maxx, maxy).bounds)`

### 可用波段

| 波段名 | 说明 |
|--------|------|
| `coastal` | B01 海岸/气溶胶 |
| `blue`    | B02 蓝 |
| `green`   | B03 绿 |
| `red`     | B04 红 |
| `rededge1`| B05 红边 1 |
| `rededge2`| B06 红边 2 |
| `rededge3`| B07 红边 3 |
| `nir`     | B08 近红外 |
| `nir08`   | B8A 窄近红外 |
| `nir09`   | B09 水汽 |
| `swir16`  | B11 短波红外 1 |
| `swir22`  | B12 短波红外 2 |
| `scl`     | 场景分类图层 |

## 编译

```bash
# 编译二进制
go build -o sentinel2-go main.go

# 运行
./sentinel2-go
```

## Docker

```bash
docker build -t sentinel2-go .
docker run --rm -v $(pwd)/sentinel2_data:/app/sentinel2_data sentinel2-go
```

## 输出

下载的文件命名格式：
```
sentinel2_data/
  S2A_50TMK_20250105_0_L2A_red.tif
  S2A_50TMK_20250105_0_L2A_green.tif
  S2A_50TMK_20250105_0_L2A_blue.tif
  ...
```

这些是标准 GeoTIFF 文件，可用 QGIS、GDAL、Python (`rioxarray`) 等打开。

## 项目结构

```
sentinel2-go/
├── main.go                   # Go 主程序
├── go.mod                    # Go 模块
├── README.md                 # 英文文档
├── README.zh.md              # 中文文档（本文件）
├── Dockerfile                # Docker 镜像
├── Makefile                  # 构建脚本
├── .gitignore                # Git 忽略规则
└── .github/workflows/go.yml  # GitHub Actions CI
```

## 常见问题

**Q: 下载失败/超时？**
- 每个 COG 文件约 50-200MB，下载时间取决于网络
- 程序默认 HTTP 超时 5 分钟，可在 `DownloadAsset` 中调整

**Q: 没有返回数据？**
- 检查日期范围是否在有效时间内（Earth Search 一般保留最近几年数据）
- 检查 bbox 是否在陆地范围内
- 尝试调高 `maxCloud` 或去掉云量过滤

**Q: 需要在 Go 中读取像素值而不是下载文件？**
- 本程序只负责"抓取"（下载）
- 如需在 Go 中读取 TIFF 像素，需引入 `github.com/airbusgeo/godal`（GDAL 的 Go 绑定），但部署需安装 GDAL C 库

## 许可证

MIT
