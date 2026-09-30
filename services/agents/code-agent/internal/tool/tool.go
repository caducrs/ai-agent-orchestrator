package tool

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	asyncv1 "github.com/caduc/ai-agent-orchestrator/contracts/async/v1"
)

func Execute(ctx context.Context, objective string) (string, []asyncv1.Evidence, []string, error) {
	root := os.Getenv("CODE_ROOT")
	if root == "" {
		root = "/workspace"
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, nil, fmt.Errorf("resolve code root: %w", err)
	}
	canonicalRoot, err = filepath.Abs(canonicalRoot)
	if err != nil {
		return "", nil, nil, err
	}
	patterns := []string{"panic(", "StatusInternalServerError", "http.Error", "return err", "error"}
	var evidence []asyncv1.Evidence
	var warnings []string
	filesRead := 0
	bytesRead := int64(0)
	err = filepath.WalkDir(canonicalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() {
			if path != canonicalRoot && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(evidence) >= 25 || filesRead >= 200 || bytesRead >= 1<<20 {
			return filepath.SkipAll
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".go" && extension != ".mod" && extension != ".yaml" && extension != ".yml" && extension != ".json" && extension != ".md" {
			return nil
		}
		canonicalPath, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			return nil
		}
		canonicalPath, resolveErr = filepath.Abs(canonicalPath)
		if resolveErr != nil {
			return nil
		}
		relative, resolveErr := filepath.Rel(canonicalRoot, canonicalPath)
		if resolveErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil
		}
		file, openErr := os.Open(canonicalPath)
		if openErr != nil {
			return nil
		}
		defer file.Close()
		filesRead++
		reader := bufio.NewReader(io.LimitReader(file, 256<<10))
		lineNumber := 0
		for {
			line, readErr := reader.ReadString('\n')
			lineNumber++
			bytesRead += int64(len(line))
			trimmed := strings.TrimSpace(line)
			for _, pattern := range patterns {
				if strings.Contains(trimmed, pattern) {
					if len(trimmed) > 240 {
						trimmed = trimmed[:240]
					}
					evidence = append(evidence, asyncv1.Evidence{Source: "code", Reference: fmt.Sprintf("%s:%d", filepath.ToSlash(relative), lineNumber), Content: trimmed})
					break
				}
			}
			if len(evidence) >= 25 || readErr != nil {
				break
			}
		}
		return nil
	})
	if err != nil {
		return "", nil, nil, err
	}
	if len(evidence) == 25 || bytesRead >= 1<<20 {
		warnings = append(warnings, "code search output was truncated")
	}
	summary := fmt.Sprintf("Code analysis inspected %d files for %q and found %d relevant locations", filesRead, objective, len(evidence))
	if len(evidence) == 0 {
		evidence = append(evidence, asyncv1.Evidence{Source: "code", Reference: filepath.ToSlash(canonicalRoot), Content: fmt.Sprintf("Inspected %d allowed files without matching configured error patterns", filesRead)})
	}
	return summary, evidence, warnings, nil
}
