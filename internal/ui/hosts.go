package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/lvjiaxuan/dnspick/internal/dnsbench"
	"github.com/lvjiaxuan/dnspick/internal/i18n"
)

// hostsFilePath returns the OS-specific path to the system hosts file.
func hostsFilePath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

// stripDnspickBlocks removes all previously written dnspick sections from
// the given hosts file content. A dnspick section starts with a line
// containing "# --- dnspick start" and ends with "# --- dnspick end ---".
// The leading blank line before a start marker (if any) is also removed.
func stripDnspickBlocks(content []byte) []byte {
	var buf bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(content))
	inBlock := false
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if !inBlock && strings.HasPrefix(line, "# --- dnspick start") {
			inBlock = true
			found = true
			// Trim the trailing blank line we may have just written.
			b := buf.Bytes()
			if len(b) > 0 && b[len(b)-1] == '\n' {
				// Check if the last line is blank.
				lastNL := bytes.LastIndexByte(b[:len(b)-1], '\n')
				if lastNL >= 0 && bytes.TrimSpace(b[lastNL+1:len(b)-1]) == nil {
					buf.Truncate(lastNL + 1)
				} else if bytes.TrimSpace(b) == nil {
					buf.Reset()
				}
			}
			continue
		}
		if inBlock {
			if strings.HasPrefix(line, "# --- dnspick end") {
				inBlock = false
			}
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	if !found {
		return content // 未发现 dnspick 区块，原样返回，避免行尾规范化导致不必要的文件写入。
	}
	return buf.Bytes()
}

// ClearOldEntries reads the hosts file, strips old dnspick blocks, and
// writes it back. If the file is unchanged (no old blocks found), this is a no-op.
func ClearOldEntries() error {
	path := hostsFilePath()

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	cleaned := stripDnspickBlocks(existing)
	if bytes.Equal(cleaned, existing) {
		return nil
	}

	if err := os.WriteFile(path, cleaned, 0644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, i18n.L().HostsCleaned, path)
	return nil
}

// WriteHostsFile writes the lowest-latency IP per domain to the system hosts
// file. Each entry is preceded by a comment line with the latency and timestamp.
// Requires port connectivity data; prints a notice and returns nil when absent.
func WriteHostsFile(results []dnsbench.Result, ports []int) error {
	m := i18n.L()

	if len(ports) == 0 {
		fmt.Fprint(os.Stderr, m.HostsNoData)
		return nil
	}

	bestPerDomain := dnsbench.CollectBestIPs(results, ports)
	if len(bestPerDomain) == 0 {
		fmt.Fprint(os.Stderr, m.HostsNoData)
		return nil
	}

	path := hostsFilePath()

	// 读取现有内容并清除旧 dnspick 块。
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	cleaned := stripDnspickBlocks(existing)

	// 构建新 dnspick 块。
	block, count := dnsbench.BuildDnspickBlock(bestPerDomain)

	// 写入：清理后内容 + 新块。
	var out bytes.Buffer
	out.Write(cleaned)
	out.Write(block)

	if err := os.WriteFile(path, out.Bytes(), 0644); err != nil {
		return err
	}

	// 回读验证，防止 Windows UAC 虚拟化导致写入被静默重定向。
	verify, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: %s", err, m.HostsWriteVerifyFailed)
	}
	if !bytes.Contains(verify, block) {
		return fmt.Errorf("%w: %s", os.ErrPermission, m.HostsWriteVerifyFailed)
	}

	fmt.Fprintf(os.Stderr, m.HostsWritten, count, path)
	// fmt.Fprintf(os.Stderr, "%s", block)
	return nil
}

// FlushDNSCache 根据操作系统执行对应的 DNS 缓存刷新命令。
// 仅支持 Windows 和 macOS，其他系统静默跳过。
func FlushDNSCache() {
	m := i18n.L()
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("ipconfig", "/flushdns")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, m.DNSFlushFailed, err)
			return
		}
		fmt.Fprint(os.Stderr, m.DNSFlushOK)
	case "darwin":
		// macOS 需要两步：dscacheutil + killall mDNSResponder，均需成功。
		if err := exec.Command("dscacheutil", "-flushcache").Run(); err != nil {
			fmt.Fprintf(os.Stderr, m.DNSFlushFailed, err)
			return
		}
		if err := exec.Command("killall", "-HUP", "mDNSResponder").Run(); err != nil {
			fmt.Fprintf(os.Stderr, m.DNSFlushFailed, err)
			return
		}
		fmt.Fprint(os.Stderr, m.DNSFlushOK)
	}
}
