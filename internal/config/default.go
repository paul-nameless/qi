package config

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func SetDefaultProfile(path, name string) error {
	if path == "" {
		path = DefaultPath()
	}
	path = ExpandHome(path)
	data, err := os.ReadFile(path)
	perm := fs.FileMode(0o600)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		data = []byte("{}\n")
	case err != nil:
		return err
	default:
		if st, err := os.Stat(path); err == nil {
			perm = st.Mode().Perm()
		}
	}
	out, err := setTopLevelString(data, "default_profile", name)
	if err != nil {
		return fmt.Errorf("%s: %w", ShortHome(path), err)
	}
	return writeFileAtomic(path, out, perm)
}

func setTopLevelString(data []byte, key, value string) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	tok, err := dec.ReadToken()
	if err != nil || tok.Kind() != '{' {
		return nil, errors.New("expected a JSON object")
	}
	open := dec.InputOffset()
	for dec.PeekKind() != '}' {
		k, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		name := k.String()
		v, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		if name == key {
			end := int(dec.InputOffset())
			start := end - len(v)
			return append(append(append([]byte{}, data[:start]...), encoded...), data[end:]...), nil
		}
	}
	entry := fmt.Sprintf("%q: %s", key, encoded)
	rest := bytes.TrimLeft(data[open:], " \t\r\n")
	if bytes.HasPrefix(rest, []byte("}")) {
		return append(append(append([]byte{}, data[:open]...), entry...), rest...), nil
	}
	return append(append(append([]byte{}, data[:open]...), "\n  "+entry+","...), data[open:]...), nil
}

func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
