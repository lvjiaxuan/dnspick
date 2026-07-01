> Fork from [palemoky/dnspick](https://github.com/palemoky/dnspick)。

公司设备及内网，不允许科学上网。问 chat 了解到，GFW 也可能有对某些 ip 地址的“抖动限流”。所以我想换 DNS 来切换可用的 ip 地址。

## 安装

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/lvjiaxuan/dnspick/main/install.ps1 | iex
```

更多安装方式（Linux / macOS / 手动下载等）[参考](https://github.com/palemoky/dnspick#installation)。

## New Feature 1: `--port`

DNS 解析 + 多端口 TCP 连通性测试（支持逗号分隔指定多个端口）。

### 使用方法

在原 `dnspick` 输出内容下方，追加端口连通性测试表格：

```sh
# 单端口（443）
dnspick --port 443 -d "github.com"

# 多端口（443 + 22）
dnspick --port 443,22 -d "github.com"
```

仅输出 DNS 解析 + 端口连通性测试表格（跳过综合测试结果和推荐）：

```sh
dnspick --port-only 443 -d "github.com"
```

### 截图

![port 运行截图](screenshot.png)

## New Feature 2: `--write` <sub>short by `-w`</sub>

将端口连通性测试中每个域名延迟最低的 IP 地址追加写入系统 hosts 文件。

需与 `--port` 配合使用（无端口数据时跳过写入）。

### 写入格式

```hosts
# --- dnspick start 2026-07-01 12:00:00 ---
# 1.2.3.4 latency 15ms 2026-07-01 12:00:00
1.2.3.4 github.com
# --- dnspick end ---
```

### 使用方法

```sh
# 测试 443 端口并将最优 IP 写入 hosts
dnspick --port 443 -d "github.com" --write

# 简写
dnspick --port 443 -d "github.com" -w
```

> **注意**：写入 hosts 文件需要管理员权限（Windows 以管理员身份运行，Linux/macOS 需 `sudo`）。

### hosts 文件位置

| 系统 | 路径 |
|------|------|
| Windows | `C:\Windows\System32\drivers\etc\hosts` |
| Linux / macOS | `/etc/hosts` |


## `configs.yml`

支持通过 [`configs.yml`](./configs.yml) 文件预设常用参数，减少重复输入。