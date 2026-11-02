package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"docibis/internal/merge"
)

// sample prints example data for a template prepared as ODT.
//
//	docibis sample template.odt
func sample(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: docibis sample template.odt")
		return 2
	}
	tpl, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	data, err := merge.Sample(tpl)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	out.SetEscapeHTML(false)
	_ = out.Encode(data)
	return 0
}

// analyze prints, for each template prepared as ODT, the engine it can use
// and why, as JSON lines.
//
//	docibis analyze templates...
func analyze(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: docibis analyze templates...")
		return 2
	}
	out := json.NewEncoder(os.Stdout)
	out.SetEscapeHTML(false)
	status := 0
	for _, file := range args {
		tpl, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			status = 1
			continue
		}
		a, err := merge.Analyze(tpl)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", file, err)
			status = 1
			continue
		}
		_ = out.Encode(struct {
			File string `json:"file"`
			merge.Analysis
		}{file, a})
	}
	return status
}

// mergeCmd fills a template prepared as ODT with JSON data.
//
//	docibis merge -data data.json -out merged.odt template.odt
func mergeCmd(args []string) int {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	dataFile := fs.String("data", "", "JSON data")
	out := fs.String("out", "merged.odt", "merged ODT")
	_ = fs.Parse(args)
	if fs.NArg() != 1 || *dataFile == "" {
		fmt.Fprintln(os.Stderr, "usage: docibis merge -data data.json -out merged.odt template.odt")
		return 2
	}
	tpl, err := os.ReadFile(fs.Arg(0))
	if err == nil {
		var data []byte
		if data, err = os.ReadFile(*dataFile); err == nil {
			var merged []byte
			start := time.Now()
			if merged, err = merge.ODT(tpl, data, time.Now()); err == nil {
				fmt.Fprintf(os.Stderr, "merged in %d µs\n", time.Since(start).Microseconds())
				err = os.WriteFile(*out, merged, 0o600)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
