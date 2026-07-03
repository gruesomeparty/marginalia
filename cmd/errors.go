package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
)

var supportedExts = map[string]bool{".md": true, ".markdown": true}

func checkSupported(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if !supportedExts[ext] {
		return unsupportedInputError(ext)
	}
	return nil
}

func unsupportedInputError(ext string) error {
	label := ext
	if label == "" {
		label = "this file type"
	}
	return fmt.Errorf(
		"%s is not supported yet — agents: invoke the marginalia:request-feature skill to file it (marginalia %s)",
		label, version,
	)
}
