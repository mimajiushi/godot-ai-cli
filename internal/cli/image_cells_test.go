package cli

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 造一张 64×80 的满行图集（r0 满宽横条 + (c1,r1) 小方块），
// 与需求文档 godot-ai-cli-req-image-grid-detect-dense-lattice 现场同构：
// 列方向无全空列 → 聚簇切不开 → suggested_grids 为空。
func writeLatticeSheet(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "lattice.png")
	img := image.NewRGBA(image.Rect(0, 0, 64, 80))
	opaque := color.RGBA{255, 80, 0, 255}
	for y := 6; y <= 12; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, opaque)
		}
	}
	for y := 23; y <= 26; y++ {
		for x := 22; x <= 25; x++ {
			img.SetRGBA(x, y, opaque)
		}
	}
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return src
}

// runImageCmd 执行 image 子命令并返回解析后的 JSON。
func runImageCmd(t *testing.T, args ...string) map[string]any {
	t.Helper()
	cmd := newImageCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("image %v: %v\n%s", args, err, buf.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, buf.String())
	}
	return payload
}

func runImageCmdErr(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newImageCommand()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err == nil {
		t.Fatalf("image %v succeeded, want error", args)
	}
	return buf.String()
}

// TestImageGridDetectEmptyCandidatesGiveReason：满行图集空候选时必须带
// reason / hints / factor_candidates（16x16 必须在因子候选里）。
func TestImageGridDetectEmptyCandidatesGiveReason(t *testing.T) {
	src := writeLatticeSheet(t)
	p := runImageCmd(t, "grid-detect", "--path", src)
	if grids, _ := p["suggested_grids"].([]any); len(grids) != 0 {
		t.Fatalf("suggested_grids = %v, want empty for the dense lattice", grids)
	}
	if p["reason"] == nil || p["reason"] == "" {
		t.Errorf("reason missing: %v", p)
	}
	hints, _ := p["hints"].([]any)
	if len(hints) == 0 {
		t.Errorf("hints missing: %v", p)
	}
	factors, _ := p["factor_candidates"].([]any)
	found := false
	for _, f := range factors {
		m, _ := f.(map[string]any)
		cell, _ := m["cell"].([]any)
		if len(cell) == 2 && cell[0].(float64) == 16 && cell[1].(float64) == 16 {
			found = true
		}
	}
	if !found {
		t.Errorf("factor_candidates must include 16x16: %v", factors)
	}
}

// TestImageGridDetectForcedCell：--cell 16x16 走逐格校验，
// r0 四格 touches_edge（跨格横条），(1,1) 小方块不贴边。
func TestImageGridDetectForcedCell(t *testing.T) {
	src := writeLatticeSheet(t)
	p := runImageCmd(t, "grid-detect", "--path", src, "--cell", "16x16")
	if p["forced"] != true || p["cols"].(float64) != 4 || p["rows"].(float64) != 5 {
		t.Fatalf("forced payload = %v", p)
	}
	touching, _ := p["touching_cells"].([]any)
	if len(touching) != 4 {
		t.Errorf("touching_cells = %v, want r0 四格", touching)
	}
	empty, _ := p["empty_cells"].([]any)
	if len(empty) != 15 {
		t.Errorf("empty_cells = %d, want 15", len(empty))
	}
}

// TestImageGridDetectForcedValidation：--cell 与 --cols/--rows 不自洽、
// 以及宽高不整除，都要结构化报错。
func TestImageGridDetectForcedValidation(t *testing.T) {
	src := writeLatticeSheet(t)
	if out := runImageCmdErr(t, "grid-detect", "--path", src, "--cell", "16x16", "--cols", "3", "--rows", "5"); !strings.Contains(out, "GRID_MISMATCH") {
		t.Errorf("inconsistent --cell/--cols want GRID_MISMATCH, got %s", out)
	}
	if out := runImageCmdErr(t, "grid-detect", "--path", src, "--cols", "3", "--rows", "5"); !strings.Contains(out, "GRID_MISMATCH") {
		t.Errorf("64 not divisible by 3 cols want GRID_MISMATCH, got %s", out)
	}
	if out := runImageCmdErr(t, "grid-detect", "--path", src, "--cols", "4"); !strings.Contains(out, "INVALID_PARAMS") {
		t.Errorf("--cols without --rows want INVALID_PARAMS, got %s", out)
	}
	// 自洽路径：--cols 4 --rows 5 反推出 16×16。
	p := runImageCmd(t, "grid-detect", "--path", src, "--cols", "4", "--rows", "5")
	cell, _ := p["cell"].([]any)
	if cell[0].(float64) != 16 || cell[1].(float64) != 16 {
		t.Errorf("cell = %v, want [16 16]", cell)
	}
}

// TestImageCellsCommand：cells 子命令输出逐格占用，--offset/--region 生效。
func TestImageCellsCommand(t *testing.T) {
	src := writeLatticeSheet(t)
	p := runImageCmd(t, "cells", "--path", src, "--cell", "16x16")
	cells, _ := p["cells"].([]any)
	if len(cells) != 5 {
		t.Fatalf("cells = %d, want 5 非空格", len(cells))
	}
	// 空格子的 bbox 字段只出现在非空格上；(1,1) 的 bbox = [6 7 4 4]。
	for _, c := range cells {
		m, _ := c.(map[string]any)
		if m["col"].(float64) == 1 && m["row"].(float64) == 1 {
			bbox, _ := m["bbox"].([]any)
			if bbox[0].(float64) != 6 || bbox[1].(float64) != 7 || bbox[2].(float64) != 4 || bbox[3].(float64) != 4 {
				t.Errorf("(1,1) bbox = %v, want [6 7 4 4]", bbox)
			}
		}
	}
	// --region 只体检 r0：16×16 一行 4 格。
	p2 := runImageCmd(t, "cells", "--path", src, "--cell", "16x16", "--region", "0,0,64,16")
	if p2["rows"].(float64) != 1 || len(p2["cells"].([]any)) != 4 {
		t.Errorf("region payload = %v", p2)
	}
	// 无 --cell 且 grid-detect 无候选 → GRID_UNDETECTED 并提示 --cell。
	if out := runImageCmdErr(t, "cells", "--path", src); !strings.Contains(out, "GRID_UNDETECTED") {
		t.Errorf("cells without --cell on undetectable sheet want GRID_UNDETECTED, got %s", out)
	}
}
