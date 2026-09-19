// Package capability 实现 v4 传输能力记录（capability record）的生成、
// 发布、读取与清理，逐字节镜像上游 src/godot_ai/transport/capability.py。
//
// 每个后端实例在自己的 HTTP 端口下发布一份私密记录
// http-<port>.json（恰好四个键：version/http/websocket/instance_nonce），
// 插件生命周期读取它来完成"认证探针 + WS 认证握手"。Windows 上目录恒为
// %LOCALAPPDATA%\godot-ai\capabilities（上游读侧在 Windows 拒绝
// GODOT_AI_CAPABILITY_DIR override，保持一致）；POSIX 走
// override/XDG/HOME，目录 0700、文件 0600。
package capability

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// 上游环境变量名（capability.py）：插件 spawn 后端时成对注入。
const (
	// HTTPEnv 是 HTTP bearer capability 的环境变量名。
	HTTPEnv = "GODOT_AI_HTTP_CAPABILITY"
	// WSEnv 是 WebSocket capability 的环境变量名（上游沿用旧名
	// GODOT_AI_WS_TOKEN 承载 v4 capability）。
	WSEnv = "GODOT_AI_WS_TOKEN"
	// DirEnv 是 capability 目录 override（Windows 上不被接受）。
	DirEnv = "GODOT_AI_CAPABILITY_DIR"
)

// recordVersion 镜像上游 RECORD_VERSION。
const recordVersion = 1

var (
	httpTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]{32,128}$`)
	wsTokenPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	noncePattern     = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// Record 是一个后端实例的完整能力记录。
type Record struct {
	// HTTP 是 /godot-ai/status 探针的 bearer token（32-128 位 bearer 字符）。
	HTTP string
	// WebSocket 是编辑器通道 HMAC capability（64 位小写 hex）。
	WebSocket string
	// InstanceNonce 标识进程实例（32 位小写 hex）；status 响应的
	// instance_id 必须等于它，清理时凭它避免误删继任者的记录。
	InstanceNonce string
}

// Validate 镜像上游 validate_record 的格式约束（含 http≠websocket）。
func (r Record) Validate() error {
	if !httpTokenPattern.MatchString(r.HTTP) {
		return errors.New("HTTP capability must be 32-128 bearer-token characters")
	}
	if !wsTokenPattern.MatchString(r.WebSocket) {
		return errors.New("WebSocket capability must be 64 lowercase hexadecimal digits")
	}
	if r.HTTP == r.WebSocket {
		return errors.New("HTTP and WebSocket capabilities must be independent")
	}
	if !noncePattern.MatchString(strings.ToLower(r.InstanceNonce)) {
		return errors.New("instance nonce must be 32 hexadecimal digits")
	}
	return nil
}

// Generate 生成一组全新随机能力（上游 generate_capabilities：
// token_urlsafe(32) + token_hex(32)；这里 http 侧用 hex 64 位——落在
// bearer 字符集内且与 ws 侧形状一致，两边都合法）。
func Generate() (Record, error) {
	httpTok, err := randomHex(32)
	if err != nil {
		return Record{}, err
	}
	wsTok, err := randomHex(32)
	if err != nil {
		return Record{}, err
	}
	nonce, err := randomHex(16)
	if err != nil {
		return Record{}, err
	}
	return Record{HTTP: httpTok, WebSocket: wsTok, InstanceNonce: nonce}, nil
}

// FromEnv 镜像上游 launch_capabilities_from_env：
// GODOT_AI_HTTP_CAPABILITY / GODOT_AI_WS_TOKEN 必须成对提供（只给一个是
// 错误）；都没给时 ok=false，调用方应 Generate。InstanceNonce 不由环境
// 提供——实例标识永远由本进程生成。
func FromEnv() (rec Record, ok bool, err error) {
	http := strings.TrimSpace(os.Getenv(HTTPEnv))
	ws := strings.TrimSpace(os.Getenv(WSEnv))
	if (http == "") != (ws == "") {
		return Record{}, false, fmt.Errorf("%s and %s must be supplied together", HTTPEnv, WSEnv)
	}
	if http == "" {
		return Record{}, false, nil
	}
	rec = Record{HTTP: http, WebSocket: ws}
	nonce, err := randomHex(16)
	if err != nil {
		return Record{}, false, err
	}
	rec.InstanceNonce = nonce
	if err := rec.Validate(); err != nil {
		return Record{}, false, err
	}
	return rec, true, nil
}

// Resolve 是生产路径：优先环境（插件 spawn daemon 时注入），否则生成。
func Resolve() (Record, error) {
	if rec, ok, err := FromEnv(); err != nil || ok {
		return rec, err
	}
	return Generate()
}

// Directory 镜像上游 capability_directory()。
// Windows：恒为 %LOCALAPPDATA%\godot-ai\capabilities（override 被拒绝——
// 插件读侧在 Windows 上同样拒绝，两边必须一致）。POSIX：override（须绝对
// 路径）> macOS Application Support > XDG_CONFIG_HOME/HOME。
func Directory() (string, error) {
	override := strings.TrimSpace(os.Getenv(DirEnv))
	if runtime.GOOS == "windows" {
		if override != "" {
			return "", fmt.Errorf("%s is not supported on Windows", DirEnv)
		}
		local := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if local == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			local = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(local, "godot-ai", "capabilities"), nil
	}
	if override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("%s must be an absolute path", DirEnv)
		}
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "godot-ai", "capabilities"), nil
	}
	config := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if config != "" {
		if !filepath.IsAbs(config) {
			return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
		}
	} else {
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "godot-ai", "capabilities"), nil
}

// RecordPath 返回某个 HTTP 端口的能力记录路径（上游 record_path）。
func RecordPath(httpPort int) (string, error) {
	if httpPort < 1 || httpPort > 65535 {
		return "", fmt.Errorf("HTTP port must be between 1 and 65535, got %d", httpPort)
	}
	dir, err := Directory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("http-%d.json", httpPort)), nil
}

// recordJSON 是记录的磁盘形状。键集合恰好四个（插件读侧逐键计数，
// 多一个少一个都拒绝）；紧凑分隔符 + 尾部换行镜像上游 write_capabilities。
type recordJSON struct {
	Version       int    `json:"version"`
	HTTP          string `json:"http"`
	WebSocket     string `json:"websocket"`
	InstanceNonce string `json:"instance_nonce"`
}

// Publish 原子发布记录：临时文件（0600、O_EXCL）+ fsync + rename，
// 镜像上游 write_capabilities。POSIX 下目录补 0700。
func Publish(httpPort int, rec Record) (string, error) {
	if err := rec.Validate(); err != nil {
		return "", err
	}
	path, err := RecordPath(httpPort)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		// 上游 _prepare_directory：目录归本用户且 0700。
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", err
		}
	}
	payload, err := json.Marshal(recordJSON{
		Version:       recordVersion,
		HTTP:          rec.HTTP,
		WebSocket:     rec.WebSocket,
		InstanceNonce: strings.ToLower(rec.InstanceNonce),
	})
	if err != nil {
		return "", err
	}
	payload = append(payload, '\n')

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			return "", err
		}
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return path, nil
}

// Read 读取一份记录（CLI 侧探针用；插件有自己的严格读侧）。文件不存在
// 或形状非法时返回 nil, nil——读不到记录不是错误，是"该端口没有 v4
// 后端"的判定依据。
func Read(httpPort int) (*Record, error) {
	path, err := RecordPath(httpPort)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw = []byte(strings.TrimSuffix(string(raw), "\n"))
	var rec recordJSON
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, nil
	}
	if rec.Version != recordVersion {
		return nil, nil
	}
	out := &Record{HTTP: rec.HTTP, WebSocket: rec.WebSocket, InstanceNonce: rec.InstanceNonce}
	if err := out.Validate(); err != nil {
		return nil, nil
	}
	return out, nil
}

// Remove 仅当记录仍指向本实例（instance_nonce 常量时间一致）时删除，
// 镜像上游 remove_capabilities：避免误删继任发布者的记录。
func Remove(httpPort int, instanceNonce string) bool {
	current, err := Read(httpPort)
	if err != nil || current == nil {
		return false
	}
	if strings.ToLower(current.InstanceNonce) != strings.ToLower(instanceNonce) {
		return false
	}
	path, err := RecordPath(httpPort)
	if err != nil {
		return false
	}
	return os.Remove(path) == nil
}

// randomHex 生成 n 字节随机数的小写 hex。
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:n]), nil
}
