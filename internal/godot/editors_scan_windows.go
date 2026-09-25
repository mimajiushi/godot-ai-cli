//go:build windows

package godot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// scanCommandTimeout 约束一次系统代理/进程枚举子命令的总时长：扫描是
// 诊断增强，绝不许把调用方拖死。
const scanCommandTimeout = 10 * time.Second

// enumeratePlatformCmdlines 用 WMI（经系统自带 powershell.exe 的
// Get-CimInstance）枚举本机进程。服务端先按命令行里的 "godot" 过滤，
// 只回传 ProcessId/CommandLine 两列，把输出压在 KB 级。powershell 不在、
// 超时或输出不可解析都返回 error——调用方按「看不到」降级。
func enumeratePlatformCmdlines() ([]processCmdline, error) {
	const ps = `Get-CimInstance Win32_Process |` +
		` Where-Object { $_.CommandLine -match 'godot' } |` +
		` Select-Object ProcessId, CommandLine | ConvertTo-Json -Compress`
	ctx, cancel := context.WithTimeout(context.Background(), scanCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps).Output()
	if err != nil {
		return nil, fmt.Errorf("powershell process enumeration: %w", err)
	}
	// ConvertTo-Json 单元素输出对象、多元素输出数组、零元素输出空——三种都收。
	var rows []struct {
		ProcessID   int    `json:"ProcessId"`
		CommandLine string `json:"CommandLine"`
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var one struct {
			ProcessID   int    `json:"ProcessId"`
			CommandLine string `json:"CommandLine"`
		}
		if err := json.Unmarshal(trimmed, &one); err != nil {
			return nil, fmt.Errorf("decode process enumeration payload: %w", err)
		}
		rows = append(rows, one)
	} else if len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, fmt.Errorf("decode process enumeration payload: %w", err)
		}
	}
	var procs []processCmdline
	for _, r := range rows {
		if r.CommandLine == "" {
			continue
		}
		procs = append(procs, processCmdline{pid: r.ProcessID, line: r.CommandLine})
	}
	return procs, nil
}

// WindowsSystemProxy 读取 HKCU Internet Settings 的系统代理
// （ProxyEnable=1 时的 ProxyServer），供 update --proxy auto 使用。
// 读注册表用系统自带 reg.exe（无 cgo/无新依赖）；未启用或读取失败都返回 ""。
func WindowsSystemProxy() string {
	ctx, cancel := context.WithTimeout(context.Background(), scanCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "reg.exe",
		"query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable").Output()
	if err != nil || !bytes.Contains(out, []byte("0x1")) {
		return ""
	}
	out, err = exec.CommandContext(ctx, "reg.exe",
		"query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyServer").Output()
	if err != nil {
		return ""
	}
	// 输出形如 "    ProxyServer    REG_SZ    127.0.0.1:7897"——取最后一个字段。
	fields := bytes.Fields(bytes.TrimSpace(out))
	if len(fields) < 3 {
		return ""
	}
	raw := string(fields[len(fields)-1])
	// http=host:port 多协议形态取 http 段；纯 host:port 直接加 scheme。
	if host, ok := cutProxyHost(raw); ok {
		return "http://" + host
	}
	return ""
}

// cutProxyHost 从 ProxyServer 值里取 HTTP 代理地址：支持 "host:port" 与
// "http=host:port;https=host:port;socks=..." 两种注册表形态。
func cutProxyHost(raw string) (string, bool) {
	for _, seg := range bytes.Split([]byte(raw), []byte(";")) {
		k, v, found := bytes.Cut(seg, []byte("="))
		if !found {
			// 纯 host:port 形态。
			return string(seg), len(seg) > 0
		}
		if strings.EqualFold(string(k), "http") && len(v) > 0 {
			return string(v), true
		}
	}
	return "", false
}
