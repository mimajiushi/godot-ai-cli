// auth.go — v4 认证握手的服务端实现。
//
// 逐字节镜像上游 src/godot_ai/transport/websocket.py 与
// protocol/envelope.py（v4.1.0）：编辑器插件先证明持有本实例的
// WebSocket capability（HMAC 挑战-应答），通过后才允许注册会话。
// v3 插件与未知对端一律 fail-closed（4002/4003），不存在降级通道。
package bridge

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	// WSProtocolVersion 是 v4 编辑器通道的唯一协议版本（上游
	// protocol/envelope.py WS_PROTOCOL_VERSION）。v3 插件的首帧
	// 是 "handshake"，会在这里被 4002 显式拒绝。
	WSProtocolVersion = 2

	// serverProofDomain / clientProofDomain 是 HMAC 转录的域分隔前缀
	// （上游 _SERVER_PROOF_DOMAIN / _CLIENT_PROOF_DOMAIN，不可改动，
	// 否则与插件侧 connection.gd 的 proof 计算不再一致）。
	serverProofDomain = "godot-ai-ws-v2/server-proof"
	clientProofDomain = "godot-ai-ws-v2/client-proof"

	// 关闭码镜像上游（RFC 6455 应用段 4000-4999）：
	closeCodeDuplicateSession  = 4001 // 重复 session_id
	closeCodeProtocolMismatch  = 4002 // 混接 v3/v4 对端或协议字段非法
	closeCodeAuthFailed        = 4003 // capability 缺失/过期/证明不符
	closeCodeHandshakePolicy   = 1008 // 握手帧非法/超时（上游 1008 policy violation）
	closeCodeHandshakeTooLarge = 1009 // 预认证帧超过 8KB 上限

	// maxHandshakeFrameBytes 是预认证阶段的帧上限（上游
	// DEFAULT_MAX_HANDSHAKE_FRAME_BYTES）；认证通过后放宽到
	// maxFrameBytes（4MB，截图负载）。
	maxHandshakeFrameBytes = 8 * 1024
)

// capabilityPattern 等校验规则镜像上游 envelope.py 的
// NonceHex/ProofHex/SessionId/VersionToken/Readiness 约束。
var (
	hex64Pattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionTokenPattern = regexp.MustCompile(`^.{1,64}$`)
	launchModePattern   = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,32}$`)
	// launchedByPattern 是 fork 扩展字段的边界（见 authResponse）。
	launchedByPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,32}$`)
	// godotVersionGate 镜像上游 _supports_v4_editor 的版本解析：
	// 4.7 用连字符（4.7-stable），旧补丁版用点号。
	godotVersionGate = regexp.MustCompile(`^(\d+)\.(\d+)(?:[.-]|$)`)
)

// authHello 是插件的第一帧（上游 AuthenticationHello，extra=forbid）。
type authHello struct {
	ClientNonce string
}

// authResponse 是插件的第三帧（上游 AuthenticatedHandshake + fork 的
// launched_by 扩展）。
type authResponse struct {
	ClientNonce      string
	ServerNonce      string
	ClientProof      string
	SessionID        string
	GodotVersion     string
	ProjectPath      string
	PluginVersion    string
	Readiness        string
	EditorPID        int
	ServerLaunchMode string
	// LaunchedBy 是 fork 扩展（上游无此字段）：编辑器进程是否由 CLI
	// spawn（GODOT_AI_CLI_LAUNCHED=1）。HasLaunchedBy 记录键是否存在，
	// 以决定按哪条转录校验 client_proof。
	LaunchedBy    string
	HasLaunchedBy bool
}

// proofMessage 镜像上游 _proof_message：domain 之后逐字段追加
// "\n<utf-8 字节长度>:<原始值>"，消除字段边界的歧义。
func proofMessage(domain string, values ...string) []byte {
	msg := []byte(domain)
	for _, v := range values {
		enc := []byte(v)
		msg = append(msg, '\n')
		msg = append(msg, strconv.Itoa(len(enc))...)
		msg = append(msg, ':')
		msg = append(msg, enc...)
	}
	return msg
}

// hmacProof 以 64 位小写 hex capability 为 key 计算 HMAC-SHA256。
func hmacProof(capability, domain string, values ...string) string {
	mac := hmac.New(sha256.New, []byte(capability))
	mac.Write(proofMessage(domain, values...))
	return hex.EncodeToString(mac.Sum(nil))
}

// serverProof 镜像 websocket_server_proof：服务端在不接触编辑器
// 元数据的前提下证明自己持有 capability。
func serverProof(capability, clientNonce, serverNonce, serverVersion string) string {
	return hmacProof(capability, serverProofDomain,
		strconv.Itoa(WSProtocolVersion), clientNonce, serverNonce, serverVersion)
}

// clientProofExpectation 计算期望的客户端证明。fork 扩展：hasLaunchedBy
// 为 true 时把 launchedBy 追加到转录末尾（插件侧 connection.gd 的 fork
// 补丁同步追加）；为 false 时按上游原样转录（纯上游 4.x 插件也能通过）。
func clientProofExpectation(capability, serverVersion string, r *authResponse, hasLaunchedBy bool) string {
	values := []string{
		strconv.Itoa(WSProtocolVersion),
		r.ClientNonce,
		r.ServerNonce,
		serverVersion,
		r.SessionID,
		r.GodotVersion,
		r.ProjectPath,
		r.PluginVersion,
		r.Readiness,
		strconv.Itoa(r.EditorPID),
		r.ServerLaunchMode,
	}
	if hasLaunchedBy {
		values = append(values, r.LaunchedBy)
	}
	return hmacProof(capability, clientProofDomain, values...)
}

// newServerNonce 生成 32 字节随机数的 hex（上游 secrets.token_hex(32)）。
func newServerNonce() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 在受支持平台上不会失败；握手随即因证明不符而失败，
		// 不会降级为可预测 nonce。
		return ""
	}
	return hex.EncodeToString(b[:])
}

// parseAuthHello 严格解析第一帧：键集合恰好是
// {type, protocol_version, client_nonce}，protocol_version 必须为 2。
// 返回错误时调用方以 code 关闭连接（镜像上游 _receive_handshake_frame
// 的失败码：超时/非 JSON 1008、超长 1009、协议不符 4002）。
func parseAuthHello(raw []byte) (*authHello, int, string) {
	if len(raw) > maxHandshakeFrameBytes {
		return nil, closeCodeHandshakeTooLarge, "v4 editor handshake frame too large"
	}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(raw, &frame); err != nil {
		return nil, closeCodeHandshakePolicy, "invalid v4 editor handshake JSON"
	}
	var typeStr string
	if err := json.Unmarshal(frame["type"], &typeStr); err != nil {
		return nil, closeCodeHandshakePolicy, "invalid v4 editor handshake object"
	}
	// v3 插件首帧是 "handshake"：用同一句指引显式拒绝（上游 fail-closed
	// 语义：混接的 v3/v4 对端不进入任何旧解析器）。
	if typeStr != "auth_hello" {
		return nil, closeCodeProtocolMismatch, fmt.Sprintf(
			"Godot AI v4 WebSocket protocol %d required", WSProtocolVersion)
	}
	if !exactKeys(frame, "type", "protocol_version", "client_nonce") {
		return nil, closeCodeProtocolMismatch, "invalid v4 auth_hello keys"
	}
	var proto int
	if err := json.Unmarshal(frame["protocol_version"], &proto); err != nil || proto != WSProtocolVersion {
		return nil, closeCodeProtocolMismatch, fmt.Sprintf(
			"Godot AI v4 WebSocket protocol %d required", WSProtocolVersion)
	}
	var nonce string
	if err := json.Unmarshal(frame["client_nonce"], &nonce); err != nil || !hex64Pattern.MatchString(nonce) {
		return nil, closeCodeProtocolMismatch, "invalid v4 auth_hello client_nonce"
	}
	return &authHello{ClientNonce: nonce}, 0, ""
}

// parseAuthResponse 严格解析第三帧。允许的唯一额外键是 fork 扩展
// launched_by（上游 extra=forbid 之外的唯一例外，且进入 proof 转录）。
func parseAuthResponse(raw []byte) (*authResponse, int, string) {
	if len(raw) > maxHandshakeFrameBytes {
		return nil, closeCodeHandshakeTooLarge, "v4 editor handshake frame too large"
	}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(raw, &frame); err != nil {
		return nil, closeCodeHandshakePolicy, "invalid v4 editor handshake JSON"
	}
	required := []string{
		"type", "protocol_version", "client_nonce", "server_nonce",
		"client_proof", "session_id", "godot_version", "project_path",
		"plugin_version", "readiness", "editor_pid", "server_launch_mode",
	}
	for _, k := range required {
		if _, ok := frame[k]; !ok {
			return nil, closeCodeProtocolMismatch, "invalid v4 auth_response keys"
		}
	}
	for k := range frame {
		if k == "launched_by" { // fork 扩展键
			continue
		}
		found := false
		for _, r := range required {
			if k == r {
				found = true
				break
			}
		}
		if !found {
			return nil, closeCodeProtocolMismatch, "invalid v4 auth_response keys"
		}
	}
	var r authResponse
	var typeStr string
	var proto int
	if err := json.Unmarshal(frame["type"], &typeStr); err != nil || typeStr != "auth_response" {
		return nil, closeCodeProtocolMismatch, "invalid v4 auth_response type"
	}
	if err := json.Unmarshal(frame["protocol_version"], &proto); err != nil || proto != WSProtocolVersion {
		return nil, closeCodeProtocolMismatch, fmt.Sprintf(
			"Godot AI v4 WebSocket protocol %d required", WSProtocolVersion)
	}
	strFields := []struct {
		key  string
		dst  *string
		rule *regexp.Regexp
	}{
		{"client_nonce", &r.ClientNonce, hex64Pattern},
		{"server_nonce", &r.ServerNonce, hex64Pattern},
		{"client_proof", &r.ClientProof, hex64Pattern},
		{"session_id", &r.SessionID, sessionIDPattern},
		{"godot_version", &r.GodotVersion, versionTokenPattern},
		{"plugin_version", &r.PluginVersion, versionTokenPattern},
		{"server_launch_mode", &r.ServerLaunchMode, launchModePattern},
	}
	for _, f := range strFields {
		if err := json.Unmarshal(frame[f.key], f.dst); err != nil || !f.rule.MatchString(*f.dst) {
			return nil, closeCodeProtocolMismatch, "invalid v4 auth_response field " + f.key
		}
	}
	if err := json.Unmarshal(frame["project_path"], &r.ProjectPath); err != nil ||
		r.ProjectPath == "" || len(r.ProjectPath) > 4096 {
		return nil, closeCodeProtocolMismatch, "invalid v4 auth_response project_path"
	}
	if err := json.Unmarshal(frame["readiness"], &r.Readiness); err != nil || !knownReadiness[r.Readiness] {
		return nil, closeCodeProtocolMismatch, "invalid v4 auth_response readiness"
	}
	var pid float64
	if err := json.Unmarshal(frame["editor_pid"], &pid); err != nil ||
		pid < 0 || pid > math.MaxInt64 || pid != float64(int64(pid)) {
		return nil, closeCodeProtocolMismatch, "invalid v4 auth_response editor_pid"
	}
	r.EditorPID = int(int64(pid))
	if lb, ok := frame["launched_by"]; ok {
		if err := json.Unmarshal(lb, &r.LaunchedBy); err != nil || !launchedByPattern.MatchString(r.LaunchedBy) {
			return nil, closeCodeProtocolMismatch, "invalid v4 auth_response launched_by"
		}
		r.HasLaunchedBy = true
	}
	return &r, 0, ""
}

// exactKeys 报告 frame 的键集合恰好等于 want（不多不少）。
func exactKeys(frame map[string]json.RawMessage, want ...string) bool {
	if len(frame) != len(want) {
		return false
	}
	for _, k := range want {
		if _, ok := frame[k]; !ok {
			return false
		}
	}
	return true
}

// supportsV4Editor 镜像上游 _supports_v4_editor：仅 4.7+ 的 4.x 线。
func supportsV4Editor(godotVersion string) bool {
	m := godotVersionGate.FindStringSubmatch(godotVersion)
	if m == nil {
		return false
	}
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return false
	}
	return major == 4 && minor >= 7
}

// validWSCapability 镜像上游 validate_ws_capability。
func validWSCapability(value string) bool {
	return hex64Pattern.MatchString(value)
}

// validHTTPCapability 镜像上游 validate_capability（32-128 位 bearer 字符集）。
var httpCapabilityPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]{32,128}$`)

func validHTTPCapability(value string) bool {
	return httpCapabilityPattern.MatchString(value)
}

// validInstanceNonce 镜像上游 validate_instance_nonce（32 hex，大小写均可，
// 归一化为小写）。
func validInstanceNonce(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, c := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
