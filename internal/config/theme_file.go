package config

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gofrs/flock"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// FileTheme reports an explicit root-level setting, without loading defaults.
func FileTheme(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var values map[string]any
	if err = toml.Unmarshal(b, &values); err != nil {
		return "", err
	}
	value, _ := values["theme"].(string)
	return value, nil
}

// SaveTheme replaces only the root value, retaining comments, formatting, and
// unrelated tables. It serializes theme writers and atomically replaces the file.
func SaveTheme(path, name string) error {
	if _, ok := BuiltinTheme(name); !ok && name != "auto" {
		return fmt.Errorf("unknown theme %q; choose dark, light, catppuccin, tokyo-night, or auto", name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(absolute))
	lock := flock.New(filepath.Join(os.TempDir(), fmt.Sprintf("maestro-theme-%x.lock", key[:16])))
	ok, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("configuration is being updated; try again")
	}
	defer lock.Close()
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	mode := os.FileMode(0o600)
	if info != nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("configuration must be a regular file: %s", path)
		}
		mode = info.Mode().Perm()
	}
	before, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	after, err := replaceTheme(before, name)
	if err != nil {
		return fmt.Errorf("update theme: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".maestro-theme-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err = file.Write(after); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	// Avoid overwriting an edit made by another editor while preparing the file.
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !bytes.Equal(current, before) {
		return fmt.Errorf("configuration changed while saving; try again")
	}
	return os.Rename(temp, path)
}

func replaceTheme(input []byte, name string) ([]byte, error) {
	var values map[string]any
	if err := toml.Unmarshal(input, &values); err != nil {
		return nil, err
	}
	if value, exists := values["theme"]; exists {
		if _, ok := value.(string); !ok {
			return nil, fmt.Errorf("theme must be a string")
		}
	}
	var parser unstable.Parser
	parser.Reset(input)
	inTable := false
	for parser.NextExpression() {
		node := parser.Expression()
		if node.Kind == unstable.Table || node.Kind == unstable.ArrayTable {
			inTable = true
		}
		if inTable || node.Kind != unstable.KeyValue {
			continue
		}
		keys := node.Key()
		if !keys.Next() || string(keys.Node().Data) != "theme" || keys.Next() {
			continue
		}
		value := node.Value()
		if value.Kind != unstable.String {
			return nil, fmt.Errorf("theme must be a string")
		}
		raw := value.Raw
		end := int(raw.Offset + raw.Length)
		output := append([]byte{}, input[:int(raw.Offset)]...)
		output = append(output, strconv.Quote(name)...)
		output = append(output, input[end:]...)
		return output, nil
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	newline := "\n"
	if bytes.Contains(input, []byte("\r\n")) {
		newline = "\r\n"
	}
	return append([]byte("theme = "+strconv.Quote(name)+newline), input...), nil
}
