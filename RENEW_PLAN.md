# Renew Pipeline 接入规划

把 `gdal_trace_outline → gdalwarp -cutline → pkRenew` 接进三个模式（Earth Search、CDSE STAC、CDSE OData）的 RGB 合成末端，自动修复 `_byte.tif` 内部 nodata 像素。

## 1. Pipeline 三步

针对一个已生成的 `*_byte.tif`：

```sh
# 1) 用 _byte.tif 自身的有效像素提取外轮廓（输出为 OGR 矢量文件）
gdal_trace_outline <byte>.tif -out-cs en -ogr-out <outline>.geojson -ogr-fmt GeoJSON

# 2) 按轮廓裁掉外侧（轮廓外置 0），全尺寸输出
gdalwarp -overwrite -cutline <outline>.geojson -dstnodata 0 <byte>.tif <masked>.tif

# 3) 在裁剪后的图上修复内部 nodata 像素
pkRenew -recover-nodata <masked>.tif <renewed>.tif

# 4) 用 renewed 原地替换 byte，并清理 outline / masked / renewed 中间产物
mv <renewed>.tif <byte>.tif
```

为何不需要「反贴」：

- gdalwarp 把轮廓外的所有像素全设为 0
- pkRenew 的修复条件是 `(r!=0 || g!=0 || b!=0) && 某波段==0`
- 轮廓外像素三波段全 0 → 不满足修复条件 → 不会被填充
- 轮廓内像素正常按原逻辑修复

## 2. 代码层接入点

只动 `gdal.go`，新增一个函数 + 在两个现有函数末尾各调用一次：

| 接入点 | 覆盖的模式 | 调用位置 |
|---|---|---|
| `BuildRGB` 末尾 | Earth Search、CDSE STAC | `_byte.tif` 生成完之后 |
| `buildRGBByte` 末尾 | CDSE OData | byte 合成完之后 |

新函数签名：

```go
func renewByteTIFF(bytePath, workDir string) error
```

外部签名只暴露 byte 路径和 workDir，pipeline 全部封在内部。

> **时序提醒**：当前 `buildRGBByte` 还没被任何地方调用（OData 的「解压 zip → 调用 buildRGBByte」还在挂起），这次改完 OData 模式不会立刻生效，要等下一步把 OData 处理流程接上才会跑到 renew。

## 3. 命名 & 中间文件管理

- 中间文件命名：以 `bytePath` 的 basename 派生，避免冲突
  - `<base>_outline.geojson`
  - `<base>_masked.tif`
  - `<base>_renew.tif`
- 写在 `workDir` 下（`BuildRGB` 传 `destDir`，`buildRGBByte` 传它已有的 `workDir` 参数）
- 三个中间文件全部 `defer os.Remove(...)`（renew 那个在成功 rename 后变成 ENOENT，无影响）
- 最终 `os.Rename(renewedPath, bytePath)` **原地替换** `_byte.tif`

## 4. 错误处理策略

renew 是「锦上添花」步骤，不应该让主流程失败：

- 任一步失败（`gdal_trace_outline` / `gdalwarp` / `pkRenew` 缺失或返回非零）→ 打印 `[renew skip] <id>: <err>`，**保留原始 `_byte.tif`**（只在最后一步 rename 才动 byte），继续下一个 item
- 成功 → 打印 `[renew] <basename>`
- `BuildRGB` / `buildRGBByte` 自身仍然返回 `nil`（不把 renew 错误向上传播）

## 5. 待确认决策点

| # | 决策点 | 倾向 | 备选 |
|---|---|---|---|
| A | outline 文件格式 | **GeoJSON**（单文件，cleanup 简单） | Shapefile（4–5 个伴生文件）/ WKT（gdalwarp `-cutline` 不一定能直接吃裸 WKT 文件，dans-gdal-scripts 的 `-wkt-out` 输出的是裸文本，要稳妥还是走 OGR 矢量） |
| B | renew 后是否原地替换 `_byte.tif` | **是**（OData 模式之前明确「最后只保留 zip 包和 _byte.tif」，三个模式保持一致） | 保留两份：`_byte.tif` + `_byte_renew.tif` |
| C | 失败行为 | **降级警告，保留原 `_byte.tif`** | 失败即视作整个 RGB 步骤失败 |
| D | 工具是否走 `findGDALTool` 查 `.exe` | 是，复用现有逻辑（Windows 模式找当前目录 + DLL，Linux 走 PATH） | 直接 `exec.Command("pkRenew", ...)` 不查 .exe |
| E | 命令名大小写 | `gdal_trace_outline`、`pkRenew`（dans-gdal-scripts / pktools 习惯） | 环境里如果叫 `pkrenew`（小写）需要告知 |
| F | `gdal_trace_outline` 的 `-ndv` 参数 | 不显式加（依赖 `_byte.tif` 自带 `-a_nodata 0` 元数据） | 显式加 `-ndv 0` 更保险 |
| G | 是否一并把 OData 的解压+合成流程也接上 | 这次只加 renew，OData 解压留到下次 | 这次一起做完 |

需要拍板的是 **A、B、E、G**。其余按倾向执行即可。

## 6. 影响面 & 验证

- 改动文件：`gdal.go`（新增 1 个函数，在 2 处末尾追加调用）
- 影响：所有三个模式生成的 `_byte.tif` 在 renew 后内部 nodata 会被修复，外侧 nodata 仍保持
- 验证：`go build ./...`、`go vet ./...`，再用一个测试 item 跑通看 `[renew]` 日志和最终文件

## 7. 工具依赖

| 工具 | 来源 | 必需性 |
|---|---|---|
| `gdal_trace_outline` | dans-gdal-scripts | 缺失 → renew 跳过，主流程不受影响 |
| `gdalwarp` | GDAL 核心 | 已假定可用（其他流程也在用 GDAL） |
| `pkRenew` | pktools | 缺失 → renew 跳过，主流程不受影响 |
