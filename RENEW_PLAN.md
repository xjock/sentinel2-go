# Renew Pipeline 接入规划

把 `gdal_trace_outline → gdalwarp -cutline → pkRenew` 接进 **OData 分支**的 RGB 合成末端，自动修复 `_byte.tif` 内部 nodata 像素，最终产物为 `*_byte_renew.tif`。

> **范围（已锁定）**：本次只动 OData 分支（即 `processODataProduct` / `buildRGBByte` 这条链路），Earth Search 与 CDSE STAC 模式（`BuildRGB`）保持不动。

## 1. Pipeline 三步

针对一个已生成的 `<destDir>/<productName>_byte.tif`：

```sh
# 1) 用 _byte.tif 自身的有效像素提取外轮廓（Shapefile，显式 -ndv 0）
gdal_trace_outline <byte>.tif -ndv 0 -out-cs en -ogr-out <outline>.shp

# 2) 按轮廓裁掉外侧（轮廓外置 0），全尺寸输出
gdalwarp -overwrite -cutline <outline>.shp -dstnodata 0 <byte>.tif <masked>.tif

# 3) 在裁剪后的图上修复内部 nodata 像素，输出最终成品
pkRenew -recover-nodata <masked>.tif <byte_renew>.tif

# 4) 成功后：删除原 _byte.tif；删除 outline shapefile 全套伴生文件、masked.tif
```

为何不需要「反贴」：

- `gdalwarp` 把轮廓外的所有像素全设为 0
- `pkRenew` 的修复条件是 `(r!=0 || g!=0 || b!=0) && 某波段==0`
- 轮廓外像素三波段全 0 → 不满足修复条件 → 不会被填充
- 轮廓内像素正常按原逻辑修复

## 2. 代码层接入点

只动两个文件：

| 文件 | 改动 |
|---|---|
| `gdal.go` | 新增 `renewByteTIFF(bytePath, outputPath, workDir) error` |
| `odata.go` | `processODataProduct` 在 `buildRGBByte` 之后调用 `renewByteTIFF`，处理 skip-if-exists 与失败回退 |

`BuildRGB`（Earth Search + CDSE STAC）**不动**。

## 3. 文件命名 & 中间产物

成品落到 `destDir`：

- `<productName>_byte.tif`：合成结果，renew 成功后删除；renew 失败时保留
- `<productName>_byte_renew.tif`：renew 后的最终成品

中间产物落到 `workDir`（即 `<destDir>/<productName>_extract/`，处理结束 `os.RemoveAll`）：

- `<productName>_byte_outline.shp/.shx/.dbf/.prj/.cpg`
- `<productName>_byte_masked.tif`

## 4. skip-if-exists 行为

`processODataProduct` 入口：

| 既有文件 | 行为 |
|---|---|
| 已有 `_byte_renew.tif` | 整个步骤跳过 |
| 仅有 `_byte.tif`（上次 renew 失败） | 复用 `_byte.tif`，重跑 renew，不重新解压 |
| 都没有 | 走完整流程：解压 → buildRGBByte → renewByteTIFF |

## 5. 错误处理（最终）

renew 是「锦上添花」步骤，不应该让主流程失败：

- `gdal_trace_outline` / `gdalwarp` / `pkRenew` 任一步失败（缺失或非零退出）→ 打印 `[renew skip] <productName>: <err>`，**保留原始 `_byte.tif`**，不生成 `_byte_renew.tif`，`processODataProduct` 仍返回 `nil`
- 成功 → 打印 `[renew] <productName>_byte_renew.tif`，并 `os.Remove(原 _byte.tif)`

## 6. 决策点（已锁定）

| # | 决策点 | 结论 |
|---|---|---|
| A | outline 文件格式 | **Shapefile**（默认 driver 输出，全部伴生文件随后清理） |
| B | 最终落盘文件 | **只保留 `_byte_renew.tif`**（renew 成功后删除 `_byte.tif`） |
| C | 失败行为 | **降级警告，保留原 `_byte.tif`** |
| D | 工具查找 | **复用 `findGDALTool`**（Windows 当前目录 + DLL 检测，Linux 走 PATH） |
| E | 命令名 | `gdal_trace_outline`、`pkRenew` |
| F | `gdal_trace_outline` 的 `-ndv` | **显式加 `-ndv 0`** |
| G | 范围 | **只对 OData 分支做 renew**，BuildRGB 不动 |

## 7. 影响面 & 验证

- 改动文件：`gdal.go`（新增 1 个函数）、`odata.go`（修改 `processODataProduct`）
- 影响：仅 OData 分支生成的 `_byte_renew.tif` 内部 nodata 被修复，外侧 nodata 仍保持
- 验证：`go build ./...`、`go vet ./...`，再用一个 OData 产品跑通看 `[renew]` 日志和最终文件

## 8. 工具依赖

| 工具 | 来源 | 必需性 |
|---|---|---|
| `gdal_trace_outline` | dans-gdal-scripts | 缺失 → renew 跳过，主流程不受影响 |
| `gdalwarp` | GDAL 核心 | 已假定可用（其他流程也在用 GDAL） |
| `pkRenew` | pktools | 缺失 → renew 跳过，主流程不受影响 |
