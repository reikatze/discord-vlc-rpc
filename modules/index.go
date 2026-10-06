package modules

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type indexManifest struct {
	Format     string         `json:"format"`
	Version    int            `json:"version"`
	Generation string         `json:"generation"`
	Counts     map[string]int `json:"counts"`
	Date       string         `json:"export_date"`
}
type indexRow struct {
	Title string  `json:"title"`
	IDs   []int64 `json:"ids"`
}

func validGeneration(s string) bool {
	if len(s) != 33 || s[0] != 'g' {
		return false
	}
	_, err := hex.DecodeString(s[1:])
	return err == nil
}
func readManifest(folder string) (indexManifest, error) {
	var m indexManifest
	err := readJSON(filepath.Join(folder, "current.json"), 16384, &m)
	if err != nil {
		return m, err
	}
	if m.Format != "discord-vlc-rpc-title-index" || m.Version != 1 || !validGeneration(m.Generation) {
		return m, errors.New("Invalid index manifest")
	}
	for _, kind := range []string{"movie", "tv"} {
		if m.Counts[kind] < 1 || m.Counts[kind] > 10000000 {
			return m, errors.New("Invalid index count")
		}
	}
	for _, kind := range []string{"movie", "tv"} {
		base := filepath.Join(folder, m.Generation+"."+kind)
		offsets, e := os.Stat(base + ".offsets")
		rows, e2 := os.Stat(base + ".rows")
		if e != nil || e2 != nil || offsets.Size() != int64(m.Counts[kind])*17 || rows.Size() == 0 {
			return m, errors.New("Index files are missing or incomplete")
		}
	}
	return m, nil
}

// indexReader pins one complete generation. It is used serially and closed
// after collecting candidates, before any network requests.
type indexMediaReader struct {
	offsets, rows *os.File
	count         int
}
type indexReader struct {
	media  map[string]indexMediaReader
	offset [17]byte
	block  [4096]byte
}

func openIndexReader(folder string, kinds ...string) (*indexReader, error) {
	m, err := readManifest(folder)
	if err != nil {
		return nil, err
	}
	r := &indexReader{media: make(map[string]indexMediaReader)}
	if len(kinds) == 0 {
		kinds = []string{"movie", "tv"}
	}
	for _, kind := range kinds {
		if kind != "movie" && kind != "tv" {
			r.close()
			return nil, errors.New("Invalid index media kind")
		}
		if _, ok := r.media[kind]; ok {
			continue
		}
		base := filepath.Join(folder, m.Generation+"."+kind)
		offsets, err := os.Open(base + ".offsets")
		if err != nil {
			r.close()
			return nil, err
		}
		rows, err := os.Open(base + ".rows")
		if err != nil {
			offsets.Close()
			r.close()
			return nil, err
		}
		r.media[kind] = indexMediaReader{offsets, rows, m.Counts[kind]}
		info, err := offsets.Stat()
		rowInfo, rowErr := rows.Stat()
		if err != nil || rowErr != nil || info.Size() != int64(m.Counts[kind])*17 || rowInfo.Size() == 0 {
			r.close()
			return nil, errors.New("Index files are missing or incomplete")
		}
	}
	return r, nil
}
func (r *indexReader) close() {
	if r == nil {
		return
	}
	for _, media := range r.media {
		media.offsets.Close()
		media.rows.Close()
	}
	r.media = nil
}
func indexCandidates(folder, title, kind string) []int64 {
	r, err := openIndexReader(folder, kind)
	if err != nil {
		return nil
	}
	defer r.close()
	return r.candidates(title, kind)
}
func indexCandidateBatch(folder string, titles, kinds []string) map[string][][]int64 {
	r, err := openIndexReader(folder, kinds...)
	if err != nil {
		return nil
	}
	defer r.close()
	matches := make(map[string][][]int64, len(kinds))
	for _, kind := range kinds {
		results := make([][]int64, len(titles))
		for i, title := range titles {
			results[i] = r.candidates(title, kind)
		}
		matches[kind] = results
	}
	return matches
}
func (r *indexReader) candidates(title, kind string) []int64 {
	if r == nil {
		return nil
	}
	media, ok := r.media[kind]
	if !ok {
		return nil
	}
	wanted := normalizeIndex(title)
	low, high := 0, media.count-1
	for low <= high {
		mid := (low + high) / 2
		buf := r.offset[:]
		if _, err := media.offsets.ReadAt(buf, int64(mid)*17); err != nil {
			return nil
		}
		at, err := strconv.ParseInt(strings.TrimSpace(string(buf)), 10, 64)
		if err != nil || at < 0 {
			return nil
		}
		block := r.block[:]
		n, err := media.rows.ReadAt(block, at)
		if err != nil && err != io.EOF {
			return nil
		}
		newline := bytes.IndexByte(block[:n], '\n')
		if newline < 0 {
			return nil
		}
		var row indexRow
		if json.Unmarshal(block[:newline], &row) != nil || len(row.IDs) > 4 {
			return nil
		}
		if row.Title == wanted {
			for _, id := range row.IDs {
				if id < 1 {
					return nil
				}
			}
			return row.IDs
		}
		if row.Title < wanted {
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return nil
}
func scanRows(path string) (*os.File, *bufio.Scanner, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 16384)
	return f, s, nil
}
func writeRun(path string, rows []string) error {
	sort.Strings(rows)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, row := range rows {
		if _, err = w.WriteString(row + "\n"); err != nil {
			f.Close()
			return err
		}
	}
	if err = w.Flush(); err == nil {
		err = f.Close()
	} else {
		f.Close()
	}
	return err
}
func mergeRuns(ctx context.Context, a, b, out string) error {
	fa, sa, err := scanRows(a)
	if err != nil {
		return err
	}
	defer fa.Close()
	fb, sb, err := scanRows(b)
	if err != nil {
		return err
	}
	defer fb.Close()
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	aa, bb := sa.Scan(), sb.Scan()
	for aa || bb {
		if err = ctx.Err(); err != nil {
			return err
		}
		if aa && (!bb || sa.Text() <= sb.Text()) {
			_, err = w.WriteString(sa.Text() + "\n")
			aa = sa.Scan()
		} else {
			_, err = w.WriteString(sb.Text() + "\n")
			bb = sb.Scan()
		}
		if err != nil {
			return err
		}
	}
	if sa.Err() != nil {
		return sa.Err()
	}
	if sb.Err() != nil {
		return sb.Err()
	}
	return w.Flush()
}
func buildIndexMedia(ctx context.Context, input io.Reader, base, kind string) (count int, err error) {
	work, err := os.MkdirTemp(filepath.Dir(base), ".index-sort-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(work)
	gz, err := gzip.NewReader(io.LimitReader(input, 512*1024*1024+1))
	if err != nil {
		return 0, err
	}
	defer gz.Close()
	decompressed := &io.LimitedReader{R: gz, N: 4*1024*1024*1024 + 1}
	scanner := bufio.NewScanner(decompressed)
	scanner.Buffer(make([]byte, 16384), 16384)
	runs := []string{}
	batch := []string{}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		path := filepath.Join(work, fmt.Sprint(len(runs)))
		if e := writeRun(path, batch); e != nil {
			return e
		}
		runs = append(runs, path)
		batch = nil
		return nil
	}
	total := 0
	for scanner.Scan() {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		var row struct {
			ID           int64  `json:"id"`
			Title        string `json:"original_title"`
			Name         string `json:"original_name"`
			Adult, Video bool
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.ID < 1 {
			return 0, errors.New("Invalid export row")
		}
		title := row.Title
		if kind == "tv" {
			title = row.Name
		}
		title = normalizeIndex(title)
		if !row.Adult && !row.Video && title != "" && len(title) <= 1024 {
			batch = append(batch, fmt.Sprintf("%s\t%016d", title, row.ID))
			total++
			if total > 10000000 {
				return 0, errors.New("Export exceeds row limit")
			}
			if len(batch) == 20000 {
				if err = flush(); err != nil {
					return 0, err
				}
			}
		}
	}
	if decompressed.N == 0 {
		return 0, errors.New("Export exceeds decompression limit")
	}
	if err = scanner.Err(); err != nil {
		return 0, err
	}
	if err = flush(); err != nil {
		return 0, err
	}
	if len(runs) == 0 {
		return 0, errors.New("Export contains no titles")
	}
	pass := 0
	for len(runs) > 1 {
		next := []string{}
		for i := 0; i < len(runs); i += 2 {
			if i+1 == len(runs) {
				next = append(next, runs[i])
				continue
			}
			out := filepath.Join(work, fmt.Sprintf("merge-%d-%d", pass, i))
			if err = mergeRuns(ctx, runs[i], runs[i+1], out); err != nil {
				return 0, err
			}
			os.Remove(runs[i])
			os.Remove(runs[i+1])
			next = append(next, out)
		}
		runs = next
		pass++
	}
	f, s, err := scanRows(runs[0])
	if err != nil {
		return 0, err
	}
	defer f.Close()
	data, err := os.Create(base + ".rows")
	if err != nil {
		return 0, err
	}
	defer data.Close()
	offsets, err := os.Create(base + ".offsets")
	if err != nil {
		return 0, err
	}
	defer offsets.Close()
	dataWriter := bufio.NewWriterSize(data, 64*1024)
	offsetWriter := bufio.NewWriterSize(offsets, 64*1024)
	position := int64(0)
	current := indexRow{IDs: []int64{}}
	overflow := false
	emit := func() error {
		if current.Title == "" {
			return nil
		}
		if overflow {
			current.IDs = []int64{}
		}
		body, e := json.Marshal(current)
		if e != nil {
			return e
		}
		if len(body) > 4094 {
			return errors.New("Index row exceeds limit")
		}
		if _, e = fmt.Fprintf(offsetWriter, "%016d\n", position); e != nil {
			return e
		}
		body = append(body, '\n')
		n, e := dataWriter.Write(body)
		position += int64(n)
		count++
		return e
	}
	for s.Scan() {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		title, number, ok := strings.Cut(s.Text(), "\t")
		id, e := strconv.ParseInt(number, 10, 64)
		if !ok || e != nil {
			return 0, errors.New("Invalid sort row")
		}
		if title != current.Title {
			if err = emit(); err != nil {
				return 0, err
			}
			current = indexRow{title, []int64{}}
			overflow = false
		}
		if len(current.IDs) == 0 || current.IDs[len(current.IDs)-1] != id {
			if len(current.IDs) < 5 {
				current.IDs = append(current.IDs, id)
			}
			if len(current.IDs) > 4 {
				overflow = true
			}
		}
	}
	if err = s.Err(); err != nil {
		return 0, err
	}
	if err = emit(); err != nil {
		return 0, err
	}
	if err = dataWriter.Flush(); err != nil {
		return 0, err
	}
	if err = offsetWriter.Flush(); err != nil {
		return 0, err
	}
	if err = data.Sync(); err != nil {
		return 0, err
	}
	if err = offsets.Sync(); err != nil {
		return 0, err
	}
	return count, nil
}
func refreshIndex(ctx context.Context, folder string, download func(context.Context, string) (io.ReadCloser, error)) error {
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	lock := filepath.Join(folder, ".go-build-lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		if info, e := os.Stat(lock); e == nil && time.Since(info.ModTime()) > time.Hour {
			os.Remove(lock)
			err = os.Mkdir(lock, 0700)
		}
		if err != nil {
			return errors.New("Another index update is running")
		}
	}
	defer os.Remove(lock)
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	generation := "g" + hex.EncodeToString(secret)
	date := time.Now().UTC().Add(-24 * time.Hour)
	m := indexManifest{"discord-vlc-rpc-title-index", 1, generation, map[string]int{}, date.Format("2006-01-02")}
	previous, _ := readManifest(folder)
	committed := false
	defer func() {
		if !committed {
			for _, kind := range []string{"movie", "tv"} {
				for _, ext := range []string{".rows", ".offsets"} {
					os.Remove(filepath.Join(folder, generation+"."+kind+ext))
				}
			}
		}
	}()
	for _, kind := range []string{"movie", "tv"} {
		prefix := "movie_ids"
		if kind == "tv" {
			prefix = "tv_series_ids"
		}
		url := "https://files.tmdb.org/p/exports/" + prefix + "_" + date.Format("01_02_2006") + ".json.gz"
		body, err := download(ctx, url)
		if err != nil {
			return err
		}
		count, err := buildIndexMedia(ctx, body, filepath.Join(folder, generation+"."+kind), kind)
		body.Close()
		if err != nil {
			return err
		}
		m.Counts[kind] = count
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	body, _ := json.Marshal(m)
	if err := atomicWrite(filepath.Join(folder, "current.json"), body, 0600); err != nil {
		return err
	}
	committed = true
	if validGeneration(previous.Generation) && previous.Generation != generation {
		for _, kind := range []string{"movie", "tv"} {
			for _, ext := range []string{".rows", ".offsets"} {
				_ = os.Remove(filepath.Join(folder, previous.Generation+"."+kind+ext))
			}
		}
	}
	return nil
}
func downloadExport(ctx context.Context, url string) (io.ReadCloser, error) {
	client := &http.Client{Timeout: 300 * time.Second}
	request, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("TMDb export download failed")
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return nil, errors.New("TMDb export unavailable")
	}
	return response.Body, nil
}
func indexDescription(folder string) string {
	m, e := readManifest(folder)
	if e != nil {
		return "No valid index"
	}
	return "Export " + m.Date
}
