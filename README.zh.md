# Sentinel-2 Go 下载器

一个轻量级的 Go 程序，支持从多个 STAC API 数据源查询并下载 Sentinel-2 L2A 卫星影像，内置交互式网页配置向导。

## 特性

- **多数据源支持**：Earth Search（公开，无需认证）、CDSE STAC（按波段）、CDSE OData（整景 ZIP）
- **网页配置向导**：首次运行自动打开浏览器页面进行配置
- **自动波段映射**：使用 `red`、`green`、`blue` 等友好名称，自动转换为各数据源对应的 Asset 键
- **断点续传**：自动跳过已下载文件，支持中断后恢复下载
- **并发下载**：可配置工作线程数
- **RGB 合成**：通过 GDAL 自动构建 RGB 合成图
- **纯 Go 实现，零外部依赖**

## 快速开始

```bash
git clone <你的仓库地址>
cd sentinel2-go
go build -o sentinel2-go main.go

# 首次运行 — 自动打开浏览器配置页面
./sentinel2-go
```

首次运行时，如果没有 `~/.sentinel2-go/settings.json`，程序会自动在本地启动 HTTP 服务并打开浏览器，引导你选择数据源和认证方式。

## 配置向导

### 首次运行（自动）

```bash
./sentinel2-go
```

如果 `~/.sentinel2-go/settings.json` 不存在，程序自动启动配置向导。

### 手动重新配置

```bash
# 网页配置（打开浏览器）
./sentinel2-go -setup

# 终端配置（无浏览器环境，适合 SSH）
./sentinel2-go -setup-auth
```

### 数据源选项

| 选项 | 说明 | 认证 |
|------|------|------|
| **Earth Search STAC API** | AWS 托管的公开 STAC，无需认证 | 无需 |
| **CDSE STAC API** | 欧盟哥白尼数据空间，按波段下载 | 用户名+密码 |
| **CDSE OData API** | 欧盟哥白尼数据空间，整景 ZIP 下载 | 用户名+密码 |
| **自定义 STAC** | 任何兼容的 STAC API 端点 | 无需 |

### CDSE 配置步骤

1. 访问 [dataspace.copernicus.eu](https://dataspace.copernicus.eu/) 注册账号
2. 查收验证邮件，点击链接完成验证
3. 在配置页面填写 CDSE 登录邮箱和密码
4. 保存并继续

配置保存在 `~/.sentinel2-go/settings.json`（文件权限 `0600`，仅所有者可读写）。

### 数据源对比

| 维度 | Earth Search STAC | CDSE STAC | CDSE OData |
|------|-------------------|-----------|------------|
| **下载粒度** | 单波段 COG（50–200 MB/波段） | 单波段 COG（50–200 MB/波段） | 整景 ZIP（500 MB–1 GB+） |
| **认证** | 无需 | 需 CDSE 账号 | 需 CDSE 账号 |
| **速度** | 快（AWS CloudFront CDN） | 中等（欧盟直链） | 慢（现场打包+大文件） |
| **国内访问** | 可能需翻墙 | 大概率免翻墙 | 大概率免翻墙 |
| **适用场景** | 快速预览、按需波段、RGB 合成 | 官方源、精确波段、免翻墙 | 需要完整原始产品包、元数据 |
| **断点续传** | 支持 | 支持 | 支持 |
| **RGB 合成** | 自动 | 自动 | 不适用（自行解压后处理） |

**选型建议：**

- **网络好、追求速度** → Earth Search STAC（默认，最快）
- **Earth Search 连不上、或需要官方源** → CDSE STAC（按波段，比 OData 快）
- **需要完整产品 ZIP（含所有波段+元数据）** → CDSE OData（慢但完整）

### `settings.json` — 认证配置

```json
{
  "source": "cdse",
  "stac_url": "https://stac.dataspace.copernicus.eu/v1",
  "collection": "sentinel-2-l2a",
  "auth": {
    "username": "your-email@example.com",
    "password": "your-password"
  }
}
```

| 字段 | 说明 |
|------|------|
| `username` | CDSE 登录邮箱 |
| `password` | CDSE 登录密码 |

## 配置说明

### `config.json` — 查询参数

```json
{
  "bbox": [116.2, 39.8, 116.6, 40.0],
  "start_date": "2026-04-01",
  "end_date": "2026-04-15",
  "max_cloud": 20.0,
  "bands": ["red", "green", "blue", "nir"],
  "limit": 20,
  "max_workers": 4,
  "max_retries": 3
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `bbox` | `[float64]` | 边界框 `[西, 南, 东, 北]` |
| `start_date` | `string` | 起始日期 `YYYY-MM-DD` |
| `end_date` | `string` | 结束日期 `YYYY-MM-DD` |
| `max_cloud` | `float64` | 最大云量百分比 (0-100) |
| `bands` | `[string]` | 要下载的波段列表（友好名称） |
| `limit` | `int` | 最大查询 STAC 条目数（默认 20） |
| `max_workers` | `int` | 并发下载线程数（默认 4） |
| `max_retries` | `int` | 失败重试次数（默认 0） |

### 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-config` | `config.json` | 查询配置文件路径 |
| `-dest` | `./sentinel2_data` | 下载文件保存目录 |
| `-setup` | — | 打开网页配置向导 |
| `-setup-auth` | — | 终端认证配置 |

### 环境变量

在 `config.json` 中可引用环境变量：

```json
{
  "auth": {
    "username": "${CDSE_USERNAME}",
    "password": "${CDSE_PASSWORD}"
  }
}
```

## 波段映射

在 `config.json` 中使用**友好名称**，程序自动映射为各数据源对应的 Asset 键。

### Earth Search 波段

| 友好名称 | Earth Search 键 | Sentinel-2 波段 |
|----------|----------------|-----------------|
| `coastal` | `coastal` | B01 |
| `blue` | `blue` | B02 |
| `green` | `green` | B03 |
| `red` | `red` | B04 |
| `rededge1` | `rededge1` | B05 |
| `rededge2` | `rededge2` | B06 |
| `rededge3` | `rededge3` | B07 |
| `nir` | `nir` | B08 |
| `nir08` | `nir08` | B8A |
| `nir09` | `nir09` | B09 |
| `swir16` | `swir16` | B11 |
| `swir22` | `swir22` | B12 |
| `scl` | `scl` | SCL |

### CDSE 波段（自动映射）

| 友好名称 | CDSE Asset 键 | 分辨率 |
|----------|---------------|--------|
| `coastal` | `B01_60m` | 60m |
| `blue` | `B02_10m` | 10m |
| `green` | `B03_10m` | 10m |
| `red` | `B04_10m` | 10m |
| `rededge1` | `B05_20m` | 20m |
| `rededge2` | `B06_20m` | 20m |
| `rededge3` | `B07_20m` | 20m |
| `nir` | `B08_10m` | 10m |
| `nir08` | `B8A_20m` | 20m |
| `nir09` | `B09_60m` | 60m |
| `swir16` | `B11_20m` | 20m |
| `swir22` | `B12_20m` | 20m |
| `scl` | `SCL_20m` | 20m |
| `aot` | `AOT_20m` | 20m |
| `wvp` | `WVP_10m` | 10m |
| `tci` | `TCI_10m` | 10m |

示例：配置 `"bands": ["red", "green", "blue"]`，程序会自动从 CDSE 下载 `B04_10m`、`B03_10m`、`B02_10m`，但文件名仍保存为 `<item>_red.tif`、`<item>_green.tif`、`<item>_blue.tif`，与 `BuildRGB` 兼容。

## 编译

```bash
go build -o sentinel2-go main.go
```

## Docker

```bash
docker build -t sentinel2-go .
docker run --rm -v $(pwd)/config.json:/app/config.json -v $(pwd)/sentinel2_data:/app/sentinel2_data sentinel2-go
```

## 输出

### STAC 模式（Earth Search / CDSE STAC）

按波段下载，文件命名格式：
```
sentinel2_data/
  S2A_50TMK_20250105_0_L2A_red.tif
  S2A_50TMK_20250105_0_L2A_green.tif
  S2A_50TMK_20250105_0_L2A_blue.tif
  ...
```

CDSE STAC 的源文件格式为 JPEG 2000（`.jp2`），但 GDAL 工具可直接读取。RGB 合成输出为 8-bit GeoTIFF。

### OData 模式（CDSE OData）

整景 ZIP 下载，文件命名格式：
```
sentinel2_data/
  S2A_T50TMK_20250105T030529_MSIL2A.zip
  ...
```

ZIP 包内含完整产品（所有波段 JP2 + XML 元数据），需自行解压后用 SNAP、ENVI 等软件处理。OData 模式下不生成 RGB 合成图。

## 常见问题

**Q: 国内用户推荐用什么数据源？**
- **首选 Earth Search**：速度最快，AWS CloudFront 全球加速，但国内部分网络可能连不上
- **Earth Search 连不上** → 切到 **CDSE STAC**：按波段下载，文件较小，欧盟官方学术站点国内大概率可直连
- **需要完整原始数据包** → 用 **CDSE OData**：整景 ZIP，慢但完整，同样大概率可直连

**Q: 下载失败/超时？**
- Earth Search / CDSE STAC：每个文件约 50-200MB，默认超时 10 分钟
- CDSE OData：整景 ZIP 通常 500MB–1GB+，单文件超时 30 分钟，建议网络稳定时使用
- 若频繁超时，增大 `config.json` 里的 `max_retries`（如设为 3 或 5）

**Q: 没有返回数据？**
- 检查日期范围是否在有效时间内
- 检查 bbox 是否在陆地范围内
- 尝试调高 `max_cloud` 或去掉云量过滤
- CDSE 数据覆盖可能与 Earth Search 不同

**Q: 如何切换数据源？**
```bash
./sentinel2-go -setup
# 网页向导里重新选择数据源并保存
```

Earth Search、CDSE STAC、CDSE OData 三种模式随时可通过 `-setup` 切换，互不影响已下载的文件。

**Q: 可以使用自定义 STAC API 吗？**
可以。在配置向导中选择"自定义 STAC API"，填写端点地址和 Collection 名称。

## 许可证

MIT
