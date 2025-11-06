package postgres

import (
	"archive/zip"
	"bytes"
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

const runScriptName = "run.sh"

var (
	errNoArtifactFiles = errors.New("no artifact files present")
	errFileNotFound    = errors.New("file not found")
)

func (r *Repository) exploitDir(exploitID string) string {
	return filepath.Join(r.artifactDir, exploitID)
}

func (r *Repository) ensureExploitDir(exploitID string) (string, error) {
	dir := r.exploitDir(exploitID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create exploit dir: %w", err)
	}
	return dir, nil
}

func (r *Repository) runScriptPath(exploitID string) string {
	return filepath.Join(r.exploitDir(exploitID), runScriptName)
}

func (r *Repository) ensureDefaultRunScript(exploitID string, entrypoint string) (string, error) {
	dir, err := r.ensureExploitDir(exploitID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, runScriptName)
	if _, err := os.Stat(path); err == nil {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read run.sh: %w", readErr)
		}
		return string(content), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat run.sh: %w", err)
	}

	script := defaultRunScript(entrypoint)
	if script == "" {
		script = defaultRunScript("python exp.py")
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return "", fmt.Errorf("write default run.sh: %w", err)
	}
	return script, nil
}

func (r *Repository) writeRunScript(exploitID string, script string) error {
	if strings.TrimSpace(script) == "" {
		return errors.New("run script cannot be empty")
	}
	dir, err := r.ensureExploitDir(exploitID)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, runScriptName)
	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	if !strings.HasPrefix(script, "#!") {
		script = "#!/bin/sh\nset -euo pipefail\n" + script
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return fmt.Errorf("write run.sh: %w", err)
	}
	return nil
}

func (r *Repository) listExploitFiles(exploitID string) ([]storage.ExploitFile, error) {
	dir := r.exploitDir(exploitID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read exploit dir: %w", err)
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
		if files[i].Name == runScriptName {
			return true
		}
		if files[j].Name == runScriptName {
			return false
		}
		return files[i].Name < files[j].Name
	})
	return files, nil
}

func (r *Repository) readExploitFile(exploitID, name string) ([]byte, error) {
	name = filepath.Base(name)
	if name == "" {
		return nil, errors.New("file name required")
	}
	path := filepath.Join(r.exploitDir(exploitID), name)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return data, nil
}

func (r *Repository) writeExploitFile(exploitID, name string, data []byte) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("file name required")
	}
	name = filepath.Base(name)
	if name == "" {
		return errors.New("invalid file name")
	}
	dir, err := r.ensureExploitDir(exploitID)
	if err != nil {
		return err
	}
	if name == runScriptName {
		return r.writeRunScript(exploitID, string(data))
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

func (r *Repository) deleteExploitFile(exploitID, name string) error {
	name = filepath.Base(name)
	if name == "" {
		return errors.New("file name required")
	}
	if name == runScriptName {
		return errors.New("run.sh cannot be deleted")
	}
	path := filepath.Join(r.exploitDir(exploitID), name)
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("file not found")
		}
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

func (r *Repository) packageExploitDir(exploitID string) ([]byte, error) {
	dir := r.exploitDir(exploitID)
	if _, err := r.ensureExploitDir(exploitID); err != nil {
		return nil, err
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
		return nil, fmt.Errorf("walk exploit dir: %w", err)
	}
	if len(files) == 0 {
		return nil, errors.New("artifact empty")
	}
	sort.Strings(files)

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	for _, rel := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			zw.Close()
			return nil, fmt.Errorf("stat %s: %w", rel, err)
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			zw.Close()
			return nil, fmt.Errorf("header %s: %w", rel, err)
		}
		header.Name = rel
		header.Method = zip.Deflate
		header.SetModTime(time.Unix(0, 0))
		writer, err := zw.CreateHeader(header)
		if err != nil {
			zw.Close()
			return nil, fmt.Errorf("zip entry %s: %w", rel, err)
		}
		file, err := os.Open(abs)
		if err != nil {
			zw.Close()
			return nil, fmt.Errorf("open %s: %w", rel, err)
		}
		if _, err := io.Copy(writer, file); err != nil {
			file.Close()
			zw.Close()
			return nil, fmt.Errorf("copy %s: %w", rel, err)
		}
		file.Close()
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalize zip: %w", err)
	}
	return buf.Bytes(), nil
}

func defaultRunScript(entry string) string {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "#!/bin/sh\nset -euo pipefail\npython exp.py \"$1\" \"$2\"\n"
	}
	if strings.HasPrefix(entry, "#!") {
		return entry + "\n"
	}
	return fmt.Sprintf("#!/bin/sh\nset -euo pipefail\n%s \"$1\" \"$2\"\n", entry)
}
