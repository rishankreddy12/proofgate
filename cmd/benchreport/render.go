package main

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

func dig(v any, path string) (any, bool) {
	cur := v
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func format(v any) string {
	f, ok := v.(float64)
	if !ok {
		return fmt.Sprint(v)
	}
	if f == math.Trunc(f) && math.Abs(f) >= 1000 {
		s := fmt.Sprintf("%.0f", f)
		var out []byte
		for i, c := range []byte(s) {
			if i > 0 && (len(s)-i)%3 == 0 {
				out = append(out, ',')
			}
			out = append(out, c)
		}
		return string(out)
	}
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", f), "0"), ".")
}

func Lookup(results map[string]any, ref string) (string, error) {
	file, path, ok := strings.Cut(ref, ":")
	if !ok {
		return "", fmt.Errorf("marker %q must be file.json:json.path", ref)
	}
	doc, ok := results[file]
	if !ok {
		return "", fmt.Errorf("no result file %q", file)
	}
	v, ok := dig(doc, path)
	if !ok {
		return "", fmt.Errorf("%s has no %q", file, path)
	}
	return format(v), nil
}

var markerRe = regexp.MustCompile(`\{\{bench:([^}]+)\}\}`)

func ResolveMarkers(text string, results map[string]any) (string, []error) {
	var errs []error
	out := markerRe.ReplaceAllStringFunc(text, func(m string) string {
		ref := markerRe.FindStringSubmatch(m)[1]
		v, err := Lookup(results, ref)
		if err != nil {
			errs = append(errs, err)
			return m
		}
		return v
	})
	return out, errs
}

func Median(files []map[string]any, path string) (float64, bool) {
	var vals []float64
	for _, f := range files {
		if v, ok := dig(f, path); ok {
			if x, ok := v.(float64); ok {
				vals = append(vals, x)
			}
		}
	}
	if len(vals) == 0 {
		return 0, false
	}
	sort.Float64s(vals)
	return vals[len(vals)/2], true
}
