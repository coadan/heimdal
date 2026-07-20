package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type dedupeArtifact struct {
	path string
	size int64
	hash [sha256.Size]byte
}

func deduplicateRunArtifacts(runDir string) (int, int64, string) {
	groups := map[int64][]dedupeArtifact{}
	for _, subtree := range []string{"test-results", "report"} {
		root := filepath.Join(runDir, subtree)
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !dedupeArtifactExtension(path) {
				return nil
			}
			info, err := entry.Info()
			if err == nil && info.Mode().IsRegular() && info.Size() >= 64*1024 {
				groups[info.Size()] = append(groups[info.Size()], dedupeArtifact{path: path, size: info.Size()})
			}
			return nil
		})
	}
	var candidates []dedupeArtifact
	for _, group := range groups {
		if len(group) > 1 {
			candidates = append(candidates, group...)
		}
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].path < candidates[right].path })
	canonical := map[[sha256.Size]byte]dedupeArtifact{}
	files := 0
	var bytes int64
	for _, candidate := range candidates {
		hash, err := hashArtifactFile(candidate.path)
		if err != nil {
			return files, bytes, truncateTraceValue(err.Error(), 500)
		}
		candidate.hash = hash
		original, exists := canonical[hash]
		if !exists {
			canonical[hash] = candidate
			continue
		}
		same, err := artifactsEqual(original.path, candidate.path)
		if err != nil {
			return files, bytes, truncateTraceValue(err.Error(), 500)
		}
		if !same {
			continue
		}
		if err := replaceWithHardLink(original.path, candidate.path); err != nil {
			return files, bytes, truncateTraceValue(err.Error(), 500)
		}
		files++
		bytes += candidate.size
	}
	return files, bytes, ""
}

func dedupeArtifactExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".zip", ".webm", ".png", ".jpg", ".jpeg":
		return true
	default:
		return false
	}
}

func hashArtifactFile(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("open artifact for deduplication: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("hash artifact for deduplication: %w", err)
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func artifactsEqual(left, right string) (bool, error) {
	leftFile, err := os.Open(left)
	if err != nil {
		return false, err
	}
	defer leftFile.Close()
	rightFile, err := os.Open(right)
	if err != nil {
		return false, err
	}
	defer rightFile.Close()
	leftBuffer, rightBuffer := make([]byte, 64*1024), make([]byte, 64*1024)
	for {
		leftN, leftErr := leftFile.Read(leftBuffer)
		rightN, rightErr := rightFile.Read(rightBuffer)
		if leftN != rightN || !bytes.Equal(leftBuffer[:leftN], rightBuffer[:rightN]) {
			return false, nil
		}
		if leftErr == io.EOF && rightErr == io.EOF {
			return true, nil
		}
		if leftErr != nil || rightErr != nil {
			return false, fmt.Errorf("compare duplicate artifacts: %v, %v", leftErr, rightErr)
		}
	}
}

func replaceWithHardLink(source, target string) error {
	temporary := target + ".heimdal-link"
	_ = os.Remove(temporary)
	if err := os.Link(source, temporary); err != nil {
		return fmt.Errorf("hard-link duplicate artifact: %w", err)
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace duplicate artifact with hard link: %w", err)
	}
	return nil
}
