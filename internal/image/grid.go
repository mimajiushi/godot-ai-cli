package image

import "image"

// GridDetectResult is the output of GridDetect: alpha-run clusters per axis
// plus plausible sprite-sheet grid candidates.
type GridDetectResult struct {
	Size           [2]int           `json:"size"`
	ColumnClusters [][2]int         `json:"column_clusters"`
	RowClusters    [][2]int         `json:"row_clusters"`
	SuggestedGrids []GridSuggestion `json:"suggested_grids"`
}

// GridSuggestion is one plausible grid: cells of Cell size starting at
// Offset, tiling into Frames columns. Exact means the cells tile the sheet
// with no remainder.
type GridSuggestion struct {
	Cell   [2]int `json:"cell"`
	Frames int    `json:"frames"`
	Offset [2]int `json:"offset"`
	Exact  bool   `json:"exact"`
}

// GridDetect finds sprite-frame grids by clustering columns/rows that carry
// any pixel above the alpha threshold, then searching for (pitch, offset)
// pairs under which every cluster fits inside exactly one cell and every
// cell holds at least one cluster. Built for the "how was this sheet really
// cut" question — e.g. 16 narrow clusters pairing up into an 8×48 grid.
func GridDetect(img image.Image, alphaThreshold float64) GridDetectResult {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	thr := uint32(clamp01(alphaThreshold) * 65535)

	cols := alphaRuns(w, func(i int) bool { return columnHasAlpha(img, i, thr) })
	rows := alphaRuns(h, func(i int) bool { return rowHasAlpha(img, i, thr) })

	colPitches := suggestPitches(cols, w)
	cellH := h
	rowOffset := 0
	if rowPitches := suggestPitches(rows, h); len(rowPitches) > 0 {
		cellH = rowPitches[0].pitch
		rowOffset = rowPitches[0].offset
	}

	grids := make([]GridSuggestion, 0, len(colPitches))
	for _, p := range colPitches {
		grids = append(grids, GridSuggestion{
			Cell:   [2]int{p.pitch, cellH},
			Frames: p.frames,
			Offset: [2]int{p.offset, rowOffset},
			Exact:  p.exact,
		})
	}
	return GridDetectResult{
		Size:           [2]int{w, h},
		ColumnClusters: cols,
		RowClusters:    rows,
		SuggestedGrids: grids,
	}
}

// alphaRuns returns the contiguous [start, end] index ranges where has(i)
// is true.
func alphaRuns(n int, has func(i int) bool) [][2]int {
	var out [][2]int
	start := -1
	for i := 0; i < n; i++ {
		if has(i) {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			out = append(out, [2]int{start, i - 1})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, [2]int{start, n - 1})
	}
	return out
}

// columnHasAlpha reports whether any pixel in column x exceeds the threshold.
func columnHasAlpha(img image.Image, x int, thr uint32) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		if _, _, _, a := img.At(x, b.Min.Y+y-b.Min.Y).RGBA(); a > thr {
			return true
		}
	}
	return false
}

// rowHasAlpha reports whether any pixel in row y exceeds the threshold.
func rowHasAlpha(img image.Image, y int, thr uint32) bool {
	b := img.Bounds()
	for x := b.Min.X; x < b.Max.X; x++ {
		if _, _, _, a := img.At(b.Min.X+x-b.Min.X, y).RGBA(); a > thr {
			return true
		}
	}
	return false
}

type pitchCandidate struct {
	pitch  int
	offset int
	frames int
	exact  bool
}

// suggestPitches enumerates (pitch, offset) grids under which every cluster
// fits inside one cell and the covered cells are contiguous and all non-empty
// (a sprite sheet has no empty frame slots). Candidates need at least two
// frames — a single catch-all cell answers nothing. Sorted: exact tilings
// first, then more frames (finer grids are the informative ones).
func suggestPitches(clusters [][2]int, extent int) []pitchCandidate {
	if len(clusters) == 0 {
		return nil
	}
	maxClusterWidth := 0
	for _, c := range clusters {
		if cw := c[1] - c[0] + 1; cw > maxClusterWidth {
			maxClusterWidth = cw
		}
	}
	firstStart := clusters[0][0]

	var out []pitchCandidate
	for pitch := maxClusterWidth; pitch <= extent/2; pitch++ {
		// offset must place cluster[0] inside the first cell:
		// o <= firstStart and firstEnd - o < pitch.
		oMin := max(0, clusters[0][1]-pitch+1)
		oMax := min(firstStart, extent-pitch) // at least one cell must fit
		for o := oMin; o <= oMax; o++ {
			frames := (extent - o) / pitch
			if frames < 2 || o+frames*pitch > extent {
				continue
			}
			if clustersFitPitch(clusters, o, pitch, frames) {
				out = append(out, pitchCandidate{
					pitch:  pitch,
					offset: o,
					frames: frames,
					exact:  o+frames*pitch == extent,
				})
			}
		}
	}
	// exact first, then larger pitch, then smaller offset
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && lessCandidate(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// lessCandidate orders candidates: exact tiling beats partial, then MORE
// frames (the finer consistent grid is the informative one — a 2-frame
// catch-all explains nothing), then smaller offset.
func lessCandidate(a, b pitchCandidate) bool {
	if a.exact != b.exact {
		return a.exact
	}
	if a.frames != b.frames {
		return a.frames > b.frames
	}
	return a.offset < b.offset
}

// clustersFitPitch checks that every cluster lies within a single cell and
// every cell between the first and last frame holds at least one cluster.
func clustersFitPitch(clusters [][2]int, offset, pitch, frames int) bool {
	cellHasCluster := make([]bool, frames)
	for _, c := range clusters {
		s, e := c[0], c[1]
		if s < offset {
			return false
		}
		cs, ce := (s-offset)/pitch, (e-offset)/pitch
		if cs != ce || cs >= frames {
			return false
		}
		cellHasCluster[cs] = true
	}
	for _, has := range cellHasCluster {
		if !has {
			return false
		}
	}
	return true
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
