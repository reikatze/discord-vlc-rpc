package modules

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func exportData(kind string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	field := "original_title"
	if kind == "tv" {
		field = "original_name"
	}
	for _, line := range []string{`{"id":7,"` + field + `":"Zulu"}`, `{"id":2,"` + field + `":"Alpha"}`, `{"id":3,"` + field + `":"Alpha"}`, `{"id":1,"` + field + `":"Excluded","adult":true}`} {
		w.Write([]byte(line + "\n"))
	}
	w.Close()
	return b.Bytes()
}
func TestStreamingIndexPublicationAndLookup(t *testing.T) {
	dir := t.TempDir()
	download := func(ctx context.Context, url string) (io.ReadCloser, error) {
		kind := "movie"
		if strings.Contains(url, "tv_series") {
			kind = "tv"
		}
		return io.NopCloser(bytes.NewReader(exportData(kind))), nil
	}
	if err := refreshIndex(context.Background(), dir, download); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"movie", "tv"} {
		if got := indexCandidates(dir, "ALPHA", kind); !reflect.DeepEqual(got, []int64{2, 3}) {
			t.Fatal(kind, got)
		}
		if got := indexCandidates(dir, "Excluded", kind); len(got) != 0 {
			t.Fatal("adult entry indexed")
		}
	}
	before, _ := os.ReadFile(filepath.Join(dir, "current.json"))
	failed := func(context.Context, string) (io.ReadCloser, error) {
		return nil, errors.New("simulated download error")
	}
	if err := refreshIndex(context.Background(), dir, failed); err == nil {
		t.Fatal("expected failure")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "current.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("partial index published")
	}
}
func TestIndexCancellationAndCorruptExport(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := buildIndexMedia(ctx, bytes.NewReader(exportData("movie")), filepath.Join(dir, "test"), "movie")
	if err == nil {
		t.Fatal("cancellation ignored")
	}
	_, err = buildIndexMedia(context.Background(), strings.NewReader("invalid gzip"), filepath.Join(dir, "test"), "movie")
	if err == nil {
		t.Fatal("invalid export accepted")
	}
}

func TestIndexExternalMergeAndAmbiguousTitle(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	for i := 21000; i > 0; i-- {
		fmt.Fprintf(gz, "{\"id\":%d,\"original_title\":\"Title %05d\"}\n", i, i)
	}
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(gz, "{\"id\":%d,\"original_title\":\"Ambiguous\"}\n", i)
	}
	gz.Close()
	dir := t.TempDir()
	base := filepath.Join(dir, "generated")
	count, err := buildIndexMedia(context.Background(), bytes.NewReader(compressed.Bytes()), base, "movie")
	if err != nil || count != 21001 {
		t.Fatal(count, err)
	}
	file, scanner, err := scanRows(base + ".rows")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if !scanner.Scan() {
		t.Fatal("empty rows")
	}
	var row indexRow
	if json.Unmarshal(scanner.Bytes(), &row) != nil || row.Title != "ambiguous" || len(row.IDs) != 0 {
		t.Fatal("ambiguity not preserved")
	}
	previous := ""
	for scanner.Scan() {
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.Title < previous {
			t.Fatal("merge is not sorted")
		}
		previous = row.Title
	}
	if scanner.Err() != nil {
		t.Fatal(scanner.Err())
	}
}

func BenchmarkIndexBuild(b *testing.B) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	for i := 30000; i > 0; i-- {
		fmt.Fprintf(gz, "{\"id\":%d,\"original_title\":\"Title %05d\"}\n", i, i)
	}
	gz.Close()
	base := filepath.Join(b.TempDir(), "benchmark")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := buildIndexMedia(context.Background(), bytes.NewReader(compressed.Bytes()), base, "movie"); err != nil {
			b.Fatal(err)
		}
	}
}

func syntheticIndex(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	download := func(ctx context.Context, url string) (io.ReadCloser, error) {
		kind := "movie"
		if strings.Contains(url, "tv_series") {
			kind = "tv"
		}
		return io.NopCloser(bytes.NewReader(exportData(kind))), nil
	}
	if err := refreshIndex(context.Background(), dir, download); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIndexReaderPinsGenerationAndCloses(t *testing.T) {
	dir := syntheticIndex(t)
	manifest, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := openIndexReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.close()
	// Change the manifest to point at a complete second generation with different IDs.
	next := manifest
	next.Generation = "g11111111111111111111111111111111"
	for _, kind := range []string{"movie", "tv"} {
		old := filepath.Join(dir, manifest.Generation+"."+kind)
		newBase := filepath.Join(dir, next.Generation+"."+kind)
		for _, ext := range []string{".rows", ".offsets"} {
			contents, err := os.ReadFile(old + ext)
			if err != nil {
				t.Fatal(err)
			}
			if ext == ".rows" {
				contents = bytes.ReplaceAll(contents, []byte("[2,3]"), []byte("[8,9]"))
			}
			if err := os.WriteFile(newBase+ext, contents, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	body, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(filepath.Join(dir, "current.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"movie", "tv"} {
		if got := reader.candidates("Alpha", kind); !reflect.DeepEqual(got, []int64{2, 3}) {
			t.Fatal("mixed generations", got)
		}
		if got := reader.candidates("Zulu", kind); !reflect.DeepEqual(got, []int64{7}) {
			t.Fatal(got)
		}
		if got := reader.candidates("missing", kind); got != nil {
			t.Fatal(got)
		}
	}
	batch := indexCandidateBatch(dir, []string{"Alpha", "Zulu", "missing"}, []string{"movie", "tv"})
	for _, kind := range []string{"movie", "tv"} {
		if !reflect.DeepEqual(batch[kind], [][]int64{{8, 9}, {7}, nil}) {
			t.Fatal("new generation not used", batch)
		}
	}
	files := reader.media["movie"]
	reader.close()
	// Stat on a closed file returns a Windows handle error rather than ErrClosed.
	// ReadAt checks the file's closed state consistently across platforms.
	var probe [1]byte
	if _, err := files.rows.ReadAt(probe[:], 0); !errors.Is(err, os.ErrClosed) {
		t.Fatal("rows remain open", err)
	}
	if _, err := files.offsets.ReadAt(probe[:], 0); !errors.Is(err, os.ErrClosed) {
		t.Fatal("offsets remain open", err)
	}
	if reader.candidates("Alpha", "movie") != nil {
		t.Fatal("closed reader used")
	}
	if reader.candidates("Alpha", "bogus") != nil {
		t.Fatal("invalid kind accepted")
	}
}

func TestIndexReaderCorruptionAndBatchFallback(t *testing.T) {
	dir := syntheticIndex(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, m.Generation+".movie")
	// A malformed row must not leak a previously decoded result through buffer reuse.
	reader, err := openIndexReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.close()
	if err := os.WriteFile(base+".rows", []byte("invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if reader.candidates("Alpha", "movie") != nil {
		t.Fatal("corrupt rows accepted")
	}
	if got := reader.candidates("Alpha", "tv"); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Fatal("other media damaged", got)
	}
	reader.close()
	if err := os.WriteFile(base+".offsets", []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := indexCandidateBatch(dir, []string{"Alpha"}, []string{"movie", "tv"}); got != nil {
		t.Fatal("incomplete generation accepted", got)
	}
}

func BenchmarkIndexCandidateBatch(b *testing.B) {
	dir := syntheticIndex(b)
	titles := []string{"Alpha", "Zulu", "missing", "Excluded"}
	kinds := []string{"movie", "tv"}
	b.Run("separate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, kind := range kinds {
				for _, title := range titles {
					indexCandidates(dir, title, kind)
				}
			}
		}
	})
	b.Run("batch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			indexCandidateBatch(dir, titles, kinds)
		}
	})
}
