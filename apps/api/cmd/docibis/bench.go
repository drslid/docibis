package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"docibis/internal/merge"
	"docibis/internal/render"
)

// workerCommand is the LibreOffice worker: DOCIBIS_WORKER when set, otherwise
// worker.py of the image, run by the Python bundled with LibreOffice.
func workerCommand() []string {
	if command := strings.Fields(os.Getenv("DOCIBIS_WORKER")); len(command) > 0 {
		return command
	}
	program := os.Getenv("LO_PATH")
	if program == "" {
		program = "/opt/libreoffice26.8/program"
	}
	return []string{filepath.Join(program, "python"), "/usr/local/lib/docibis/worker.py"}
}

// bench converts files with warm workers and prints the times.
//
//	docibis bench [-workers 1] [-runs 10] [-format pdf] [-odt] [-parallel 3] files...
func bench(args []string) int {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	workers := fs.Int("workers", 1, "LibreOffice workers")
	runs := fs.Int("runs", 10, "measured conversions per file, after one warm-up")
	format := fs.String("format", "pdf", "output format: pdf, docx, odt")
	filter := fs.String("filter", "", "export options, for example PDF/A")
	odt := fs.Bool("odt", false, "prepare each file as ODT first, then measure the ODT conversion")
	sampled := fs.Bool("sample", false, "with -odt: fill each template with example data before each conversion")
	keep := fs.String("keep", "", "directory where the last output of each file is copied")
	parallel := fs.Int("parallel", 0, "also convert this many files at the same time (a subscription is 3)")
	_ = fs.Parse(args)
	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "usage: docibis bench [flags] files...")
		return 2
	}
	command := workerCommand()
	ctx := context.Background()
	start := time.Now()
	level := slog.LevelWarn
	if os.Getenv("DOCIBIS_BENCH_DEBUG") != "" {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	pool, err := render.Start(ctx, render.Options{Command: command, Workers: *workers, QueueSize: 1000, Timeout: time.Minute, Logger: logger})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer pool.Close()
	fmt.Printf("%d worker(s) ready in %d ms\n", *workers, time.Since(start).Milliseconds())

	tmp, err := os.MkdirTemp("", "docibis-bench-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)

	sources := make([]string, 0, len(files))
	for _, f := range files {
		src, err := localCopy(tmp, f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
			return 1
		}
		if *odt && !strings.EqualFold(filepath.Ext(src), ".odt") {
			prepared := filepath.Join(tmp, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+".odt")
			if _, err := pool.Convert(ctx, src, prepared, "odt", ""); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
				return 1
			}
			src = prepared
		}
		sources = append(sources, src)
	}

	fmt.Printf("%-32s %8s %8s %8s %8s   (ms, %s, %d runs)\n", "file", "p50", "p95", "min", "max", *format, *runs)
	tpls, datas := make([][]byte, len(sources)), make([][]byte, len(sources))
	for i, src := range sources {
		out := filepath.Join(tmp, fmt.Sprintf("out-%d.%s", i, *format))
		var tpl, data []byte
		if *sampled {
			if tpl, err = os.ReadFile(src); err == nil {
				var d map[string]any
				if d, err = merge.Sample(tpl); err == nil {
					data, err = json.Marshal(d)
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(src), err)
				return 1
			}
			tpls[i], datas[i] = tpl, data
		}
		var times, merges []time.Duration
		for r := 0; r <= *runs; r++ {
			in := src
			t0 := time.Now()
			if *sampled {
				merged, err := merge.ODT(tpl, data, time.Now())
				if err == nil {
					in = filepath.Join(tmp, fmt.Sprintf("merged-%d.odt", i))
					err = os.WriteFile(in, merged, 0o600)
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(src), err)
					return 1
				}
			}
			merged := time.Since(t0)
			d, err := pool.Convert(ctx, in, out, *format, *filter)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(src), err)
				return 1
			}
			if r > 0 {
				times = append(times, d+merged)
				merges = append(merges, merged)
			}
		}
		printStats(filepath.Base(src), times)
		if *sampled {
			printStats("  of which merge", merges)
		}
		if *keep != "" {
			if b, err := os.ReadFile(out); err == nil {
				name := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)) + "." + *format
				_ = os.WriteFile(filepath.Join(*keep, name), b, 0o644)
			}
		}
	}

	if *parallel > 0 {
		var times []time.Duration
		for r := 0; r < *runs; r++ {
			t0 := time.Now()
			var wg sync.WaitGroup
			for i := range *parallel {
				wg.Add(1)
				go func() {
					defer wg.Done()
					j := i % len(sources)
					in, out := sources[j], filepath.Join(tmp, fmt.Sprintf("par-%d-%d.%s", r, i, *format))
					if *sampled {
						merged, err := merge.ODT(tpls[j], datas[j], time.Now())
						if err == nil {
							in = filepath.Join(tmp, fmt.Sprintf("par-%d-%d.odt", r, i))
							err = os.WriteFile(in, merged, 0o600)
						}
						if err != nil {
							fmt.Fprintln(os.Stderr, err)
							return
						}
					}
					if _, err := pool.Convert(ctx, in, out, *format, *filter); err != nil {
						fmt.Fprintln(os.Stderr, err)
					}
				}()
			}
			wg.Wait()
			times = append(times, time.Since(t0))
		}
		printStats(fmt.Sprintf("%d at the same time", *parallel), times)
	}
	return 0
}

// localCopy returns a local path for a file, downloading https:// ones.
func localCopy(dir, file string) (string, error) {
	if !strings.HasPrefix(file, "https://") {
		return filepath.Abs(file)
	}
	u, err := url.Parse(file)
	if err != nil {
		return "", err
	}
	resp, err := http.Get(file)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s", resp.Status)
	}
	path := filepath.Join(dir, filepath.Base(u.Path))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return path, err
}

func printStats(name string, times []time.Duration) {
	slices.Sort(times)
	pct := func(p float64) int64 {
		i := int(p*float64(len(times)) + 0.5)
		return times[min(max(i-1, 0), len(times)-1)].Milliseconds()
	}
	fmt.Printf("%-32s %8d %8d %8d %8d\n", name, pct(0.5), pct(0.95), times[0].Milliseconds(), times[len(times)-1].Milliseconds())
}
