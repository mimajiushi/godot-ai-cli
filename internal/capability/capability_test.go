package capability_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mimajiushi/godot-ai-cli/internal/capability"
)

// TestGenerateAndValidate：生成的记录满足上游 validate_record 的全部
// 格式约束（http 32-128 bearer 字符、ws 64hex、nonce 32hex、两 token 独立）。
func TestGenerateAndValidate(t *testing.T) {
	rec, err := capability.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("generated record invalid: %v", err)
	}
	// 两次生成必须不同（随机性冒烟）。
	other, _ := capability.Generate()
	if other.HTTP == rec.HTTP || other.WebSocket == rec.WebSocket {
		t.Error("two Generate calls produced identical tokens")
	}
}

// TestFromEnvPairing 镜像上游 launch_capabilities_from_env：
// 两个环境变量必须成对；单给一个是错误；都缺时 ok=false。
func TestFromEnvPairing(t *testing.T) {
	t.Setenv(capability.HTTPEnv, "")
	t.Setenv(capability.WSEnv, "")

	if _, ok, err := capability.FromEnv(); err != nil || ok {
		t.Errorf("empty env: ok=%v err=%v, want false/nil", ok, err)
	}

	t.Setenv(capability.HTTPEnv, strings.Repeat("a", 43))
	if _, _, err := capability.FromEnv(); err == nil {
		t.Error("http-only env must be an error (pairing rule)")
	}

	t.Setenv(capability.WSEnv, strings.Repeat("b", 64))
	rec, ok, err := capability.FromEnv()
	if err != nil || !ok {
		t.Fatalf("paired env: ok=%v err=%v", ok, err)
	}
	if rec.HTTP != strings.Repeat("a", 43) || rec.WebSocket != strings.Repeat("b", 64) {
		t.Errorf("env tokens not adopted: %+v", rec)
	}
	if rec.InstanceNonce == "" {
		t.Error("instance nonce must be generated even with env tokens")
	}

	// 坏形状被拒。
	t.Setenv(capability.WSEnv, "not-hex")
	if _, _, err := capability.FromEnv(); err == nil {
		t.Error("malformed ws capability must be rejected")
	}
}

// TestPublishReadRemoveRoundtrip：发布→读取→按 nonce 清理的完整闭环，
// 含磁盘形状（恰好四键、尾部换行、ASCII）。
func TestPublishReadRemoveRoundtrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows 的目录由 LOCALAPPDATA 决定；t.Setenv 重定向它即可。
		t.Setenv("LOCALAPPDATA", t.TempDir())
	} else {
		t.Setenv(capability.DirEnv, t.TempDir())
	}

	rec, err := capability.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// 用一个测试内不会真监听的端口（记录只是文件）。
	port := 45991
	path, err := capability.Publish(port, rec)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// 磁盘形状：恰好四键、紧凑 JSON、尾部换行、纯 ASCII。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read published record: %v", err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("record must end with a newline (上游 write_capabilities)")
	}
	for _, b := range raw {
		if b > 127 {
			t.Fatal("record must be pure ASCII")
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw[:len(raw)-1], &parsed); err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}
	if len(parsed) != 4 || parsed["version"] != float64(1) {
		t.Errorf("record keys = %v, want exactly version/http/websocket/instance_nonce", parsed)
	}

	// 读回一致。
	got, err := capability.Read(port)
	if err != nil || got == nil {
		t.Fatalf("Read: %v %v", got, err)
	}
	if *got != rec {
		t.Errorf("roundtrip mismatch: got %+v want %+v", got, rec)
	}

	// 错误 nonce 删不掉；正确 nonce 才删得掉。
	if capability.Remove(port, strings.Repeat("0", 32)) {
		t.Error("Remove with a wrong nonce must refuse (继任者保护)")
	}
	if !capability.Remove(port, rec.InstanceNonce) {
		t.Error("Remove with the instance nonce must succeed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("record file still present after Remove")
	}
}

// TestReadAbsentAndGarbage：读不到记录不是错误——返回 nil。
func TestReadAbsentAndGarbage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", t.TempDir())
	} else {
		t.Setenv(capability.DirEnv, t.TempDir())
	}
	if rec, err := capability.Read(45992); err != nil || rec != nil {
		t.Errorf("absent record: rec=%v err=%v, want nil/nil", rec, err)
	}

	// 垃圾内容同样读为 nil（fail-closed 的读侧）。
	path, err := capability.RecordPath(45993)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2,"http":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec, err := capability.Read(45993); err != nil || rec != nil {
		t.Errorf("garbage record: rec=%v err=%v, want nil/nil", rec, err)
	}
}

// TestDirectoryWindowsIgnoresOverride：Windows 上 override 被拒绝
// （与插件读侧一致——否则两边会读不同的目录）。
func TestDirectoryWindowsIgnoresOverride(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only rule")
	}
	t.Setenv(capability.DirEnv, `D:\elsewhere`)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if _, err := capability.Directory(); err == nil {
		t.Error("override on Windows must be rejected")
	}
}
