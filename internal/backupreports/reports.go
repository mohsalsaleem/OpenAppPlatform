// Package backupreports reads bounded, redacted reports from a trusted host helper.
// It never opens backup archives, executes backups, or starts restored controllers.
package backupreports

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

type Report struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
	Source    string    `json:"source"`
	Coverage  string    `json:"coverage"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	SHA256    string    `json:"sha256"`
	SizeBytes int64     `json:"sizeBytes"`
	ErrorCode string    `json:"errorCode"`
}
type Overview struct {
	Configured     bool     `json:"configured"`
	Reports        []Report `json:"reports"`
	InvalidReports int      `json:"invalidReports"`
	Truncated      bool     `json:"truncated"`
}

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (r Report) valid() bool {
	if r.Version != 1 || !idPattern.MatchString(r.ID) || (r.Action != "backup" && r.Action != "restore-check") || r.Source != "host-helper" || r.Coverage != "metadata-only" || r.StartedAt.IsZero() || r.UpdatedAt.Before(r.StartedAt) || r.SizeBytes < 0 {
		return false
	}
	switch r.Status {
	case "running":
		return r.SHA256 == "" && r.SizeBytes == 0 && r.ErrorCode == ""
	case "succeeded":
		return digestPattern.MatchString(r.SHA256) && r.SizeBytes > 0 && r.ErrorCode == ""
	case "failed":
		switch r.ErrorCode {
		case "controller-running", "destination-exists", "checksum-mismatch", "invalid-restore-target", "interrupted", "operation-failed":
			return r.SHA256 == "" && r.SizeBytes == 0
		}
	}
	return false
}
func Read(directory string) (Overview, error) {
	out := Overview{Configured: directory != "", Reports: []Report{}}
	if directory == "" {
		return out, nil
	}
	if !filepath.IsAbs(directory) {
		return out, errors.New("backup report directory must be an absolute trusted path")
	}
	// os.Root fences symlink/path escapes; only regular report files are accepted.
	root, err := os.OpenRoot(directory)
	if err != nil {
		return out, errors.New("backup reports are unavailable; check the read-only mount")
	}
	defer root.Close()
	folder, err := root.Open(".")
	if err != nil {
		return out, err
	}
	defer folder.Close()
	entries, err := folder.ReadDir(501)
	if err != nil && err != io.EOF {
		return out, errors.New("cannot read backup reports")
	}
	if len(entries) > 500 {
		entries = entries[:500]
		out.Truncated = true
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !idPattern.MatchString(name[:max(0, len(name)-5)]) || filepath.Ext(name) != ".json" {
			continue
		}
		file, e := root.Open(name)
		if e != nil {
			out.InvalidReports++
			continue
		}
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 {
			file.Close()
			out.InvalidReports++
			continue
		}
		raw, e := io.ReadAll(io.LimitReader(file, (16<<10)+1))
		file.Close()
		if e != nil || len(raw) > 16<<10 {
			out.InvalidReports++
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var report Report
		if decoder.Decode(&report) != nil || !report.valid() || name != report.ID+".json" {
			out.InvalidReports++
			continue
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			out.InvalidReports++
			continue
		}
		out.Reports = append(out.Reports, report)
	}
	sort.Slice(out.Reports, func(i, j int) bool { return out.Reports[i].UpdatedAt.After(out.Reports[j].UpdatedAt) })
	if len(out.Reports) > 100 {
		out.Reports = out.Reports[:100]
		out.Truncated = true
	}
	return out, nil
}
