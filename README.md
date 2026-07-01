> Fork from [palemoky/dnspick](https://github.com/palemoky/dnspick)。


# 动机

公司设备及内网，不允许科学上网。问 chat 了解到，GFW 也可能有对某些 ip 地址的“抖动限流”。所以我想换 DNS 来切换可用的 ip 地址。

# 安装

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/lvjiaxuan/dnspick/main/install.ps1 | iex
```

更多安装方式（Linux / macOS / 手动下载等）[参考](https://github.com/palemoky/dnspick#installation)。

直接开始使用：

```sh
dnspick --port-only 443 -d "github.com" -w --interval 1
```


# Feature 1: `--port-only`

查看 DNS 域名解析结果 + 端口 TCP 连通性测试。

## 使用方法

在原 `dnspick` 输出内容下方，追加端口连通性测试表格：

```sh
# 单端口（443）
dnspick --port 443 -d "github.com"

# multiple
dnspick --port 443,22 -d "github.com,google.com"
```

仅输出 DNS 解析 + 端口连通性测试表格（跳过原 dnspicker 表格）：

```sh
dnspick --port-only 443 -d "github.com"
```

## 截图

![port 运行截图](screenshot.png)


# Feature 2: `--write` <sub>short by `-w`</sub>

将端口连通性测试中每个域名延迟最低的 IP 地址追加写入系统 hosts 文件。

需与 `--port` 配合使用（无端口数据时跳过写入）。

## 写入示例

```hosts
# --- dnspick start 2026-07-01 12:00:00 ---
# 1.2.3.4 latency 15ms 2026-07-01 12:00:00
1.2.3.4 github.com
# --- dnspick end ---
```

## 使用方法

```sh
# 测试 443 端口并将最优 IP 写入 hosts
dnspick --port 443 -d "github.com" --write

# 简写
dnspick --port 443 -d "github.com" -w
```

> **注意**：写入 hosts 文件需要管理员权限（Windows 以管理员身份运行，Linux/macOS 需 `sudo`）。

# Feature 3: `--interval` 轮询模式

指定时间间隔（分钟），让 dnspick 自动循环执行「DNS 解析 + 端口测试」，直到手动退出。

## 使用方法

```sh
# 每 5 分钟自动重新解析和测试
dnspick --port 443 -d "github.com" --interval 5
```

## 行为说明

- **最小值**：`1` 分钟；值为 `0` 或不传该参数表示仅执行一次（默认行为）。
- **轮次横幅**：每轮开始时打印 `═══ 🔄 Round #N | 时间戳 ═══`，便于区分各轮结果。
- **实时倒计时**（仅 TTY 终端）：轮询等待期间显示剩余时间并原地刷新。
- **手动触发**（仅 TTY 终端）：等待期间按 `u` 键立即跳过倒计时，马上开始下一轮查询。
- **退出**：按 `Ctrl+C` 中断轮询并安全退出，终端会打印已完成的总轮次数。

# hosts 文件位置

| 系统 | 路径 |
|------|------|
| Windows | `C:\Windows\System32\drivers\etc\hosts` |
| Linux / macOS | `/etc/hosts` |

# 配置文件

支持通过用户目录下的 `dnspick-config.yml` 文件预先配置 DNS 服务器和待解析域名，避免每次都在命令行中重复输入。

| 系统 | 配置文件路径 |
|------|------|
| Windows | `C:\Users\<用户名>\dnspick-config.yml` |
| Linux / macOS | `~/dnspick-config.yml` |

配置示例可参考 [dnspicker-config.yml](./dnspicker-config.yml)。

若用户目录下不存在该配置文件，程序将使用内置的默认配置。
