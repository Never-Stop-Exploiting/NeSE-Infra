package postgres

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"awdinfra/internal/storage"
)

const (
	submitterDirName   = "submitter"
	submitterScript    = "submit.sh"
	defaultSubmitEntry = `exec python submit.py "$1"`
)

func (r *Repository) submitterDir() string {
	return filepath.Join(r.artifactDir, submitterDirName)
}

func (r *Repository) ensureSubmitterDir() (string, error) {
	dir := r.submitterDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create submitter dir: %w", err)
	}
	return dir, nil
}

func (r *Repository) submitterScriptPath() string {
	return filepath.Join(r.submitterDir(), submitterScript)
}

func (r *Repository) ensureSubmitterScript() (string, error) {
	dir, err := r.ensureSubmitterDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, submitterScript)
	if _, err := os.Stat(path); err == nil {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read submit.sh: %w", readErr)
		}
		return string(content), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat submit.sh: %w", err)
	}
	script := defaultSubmitScript()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return "", fmt.Errorf("write submit.sh: %w", err)
	}
	return script, nil
}

func (r *Repository) writeSubmitterScript(script string) error {
	if strings.TrimSpace(script) == "" {
		return errors.New("submit.sh cannot be empty")
	}
	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	if !strings.HasPrefix(script, "#!") {
		script = "#!/bin/sh\nset -euo pipefail\n" + script
	}
	dir, err := r.ensureSubmitterDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, submitterScript)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return fmt.Errorf("write submit.sh: %w", err)
	}
	return nil
}

func (r *Repository) ListSubmitterFiles(ctx context.Context) ([]storage.ExploitFile, error) {
	_ = ctx
	dir := r.submitterDir()
	if _, err := r.ensureSubmitterDir(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read submitter dir: %w", err)
	}
	files := make([]storage.ExploitFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", name, err)
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		sum := md5.Sum(data)
		files = append(files, storage.ExploitFile{
			Name:    name,
			Size:    info.Size(),
			MD5:     fmt.Sprintf("%x", sum[:]),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Name == submitterScript {
			return true
		}
		if files[j].Name == submitterScript {
			return false
		}
		return files[i].Name < files[j].Name
	})
	return files, nil
}

func (r *Repository) SaveSubmitterFile(ctx context.Context, name string, data []byte) error {
	_ = ctx
	if strings.TrimSpace(name) == "" {
		return errors.New("file name required")
	}
	name = filepath.Base(name)
	if name == "" {
		return errors.New("invalid file name")
	}
	dir, err := r.ensureSubmitterDir()
	if err != nil {
		return err
	}
	if name == submitterScript {
		return r.writeSubmitterScript(string(data))
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func (r *Repository) DeleteSubmitterFile(ctx context.Context, name string) error {
	_ = ctx
	name = filepath.Base(name)
	if name == "" {
		return errors.New("file name required")
	}
	if name == submitterScript {
		return errors.New("submit.sh cannot be deleted")
	}
	path := filepath.Join(r.submitterDir(), name)
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("file not found")
		}
		return fmt.Errorf("delete %s: %w", name, err)
	}
	return nil
}

func (r *Repository) ReadSubmitterFile(ctx context.Context, name string) ([]byte, error) {
	_ = ctx
	name = filepath.Base(name)
	if name == "" {
		return nil, errors.New("file name required")
	}
	path := filepath.Join(r.submitterDir(), name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errFileNotFound
		}
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return data, nil
}

func (r *Repository) GetSubmitterScript(ctx context.Context) (string, error) {
	return r.ensureSubmitterScript()
}

func (r *Repository) SaveSubmitterScript(ctx context.Context, script string) error {
	return r.writeSubmitterScript(script)
}

func (r *Repository) SubmitterArtifact(ctx context.Context) ([]byte, string, error) {
	_ = ctx
	dir := r.submitterDir()
	if _, err := r.ensureSubmitterDir(); err != nil {
		return nil, "", err
	}
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("walk submitter dir: %w", err)
	}
	if len(files) == 0 {
		return nil, "", errors.New("submitter package is empty")
	}
	sort.Strings(files)

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	hash := md5.New()
	for _, rel := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			zw.Close()
			return nil, "", fmt.Errorf("stat %s: %w", rel, err)
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			zw.Close()
			return nil, "", fmt.Errorf("header %s: %w", rel, err)
		}
		header.Name = rel
		header.Method = zip.Deflate
		header.SetModTime(time.Unix(0, 0))
		writer, err := zw.CreateHeader(header)
		if err != nil {
			zw.Close()
			return nil, "", fmt.Errorf("zip %s: %w", rel, err)
		}
		file, err := os.Open(abs)
		if err != nil {
			zw.Close()
			return nil, "", fmt.Errorf("open %s: %w", rel, err)
		}
		if _, err := io.Copy(io.MultiWriter(writer, hash), file); err != nil {
			file.Close()
			zw.Close()
			return nil, "", fmt.Errorf("copy %s: %w", rel, err)
		}
		file.Close()
	}
	if err := zw.Close(); err != nil {
		return nil, "", fmt.Errorf("finalize zip: %w", err)
	}
	version := fmt.Sprintf("%x", hash.Sum(nil))
	return buf.Bytes(), version, nil
}

func defaultSubmitScript() string {
	return fmt.Sprintf("#!/bin/sh\nset -euo pipefail\n%s\n", defaultSubmitEntry)
}
