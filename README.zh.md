# Sentinel-2 Go 下载器

一个轻量级的 Go 程序，用于从 [Earth Search](https://earth-search.aws.element84.com/) STAC API 查询并下载公开的 Sentinel-2 L2A 卫星影像，**无需任何认证**。

## 特性

- 无需 API Key 或凭证
- 支持按边界框、日期范围、云量过滤查询
- 下载单波段 Cloud Optimized GeoTIFF (COG) 文件
- 自动跳过已下载文件
- 纯 Go 实现，零外部依赖
- JSON 配置文件驱动
- 命令行参数指定输出目录

## 环境要求

- [Go](https://go.dev/) 1.21 或更高版本

## 快速开始

```bash
git clone <你的仓库地址>
cd sentinel2-go

# 编辑 config.json 设置你的查询区域和日期
go run main.go -config config.json -dest ./sentinel2_data
```

程序会：
1. 从 `config.json` 加载查询参数
2. 根据配置的边界框和日期范围搜索 Sentinel-2 L2A 数据
3. 按云量过滤结果
4. 将请求的波段下载到指定目录

## 配置

创建 `config.json` 文件（参考 `config.json`）：

```json
{
  "bbox": [116.2, 39.8, 116.6, 40.0],
  "start_date": "2025-01-01",
  "end_date": "2025-01-15",
  "max_cloud": 20.0,
  "bands": ["red", "green", "blue", "nir"],
  "limit": 20
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `bbox` | `[float64]` | 边界框 `[西, 南, 东, 北]` |
| `start_date` | `string` | 起始日期 `YYYY-MM-DD` |
| `end_date` | `string` | 结束日期 `YYYY-MM-DD` |
| `max_cloud` | `float64` | 最大云量百分比 (0-100) |
| `bands` | `[string]` | 要下载的波段列表 |
| `limit` | `int` | 最大查询 STAC 条目数（默认 20） |

### 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-config` | `config.json` | 配置文件路径 |
| `-dest` | `./sentinel2_data` | 下载文件保存目录 |

### 使用示例

```bash
# 使用默认配置文件和默认输出目录
go run main.go

# 使用自定义配置和输出目录
go run main.go -config beijing.json -dest /data/s2_beijing

# 运行编译后的二进制文件
./sentinel2-go -config europe.json -dest ./europe_s2
```

### 获取边界框

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
# 编译二进制文件
go build -o sentinel2-go main.go

# 运行
./sentinel2-go -config config.json -dest ./output
```

## Docker

```bash
docker build -t sentinel2-go .
docker run --rm -v $(pwd)/config.json:/app/config.json -v $(pwd)/sentinel2_data:/app/sentinel2_data sentinel2-go
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
├── config.json               # 查询配置示例
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
- 尝试调高 `max_cloud` 或去掉云量过滤

**Q: 需要在 Go 中读取像素值而不是下载文件？**
- 本程序只负责"抓取"（下载）
- 如需在 Go 中读取 TIFF 像素，需引入 `github.com/airbusgeo/godal`（GDAL 的 Go 绑定），但部署需安装 GDAL C 库

## 许可证

MIT
